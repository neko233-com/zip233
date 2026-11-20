package archive

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"time"
	"unicode/utf8"

	"github.com/mholt/archives"
)

// WinZip AE-2 constants (see the WinZip AES APPNOTE).
const (
	zipMethodStore    = uint16(0)
	zipMethodDeflate  = uint16(8)
	zipMethodAES      = uint16(99) // pseudo-method: real method lives in extra field
	zipAESExtraID     = uint16(0x9901)
	zipAESVersion     = uint16(2) // AE-2: CRC is zeroed, HMAC is authoritative
	zipAESStrength256 = byte(3)
	zipAESSaltLen     = 16 // AES-256 salt length
	zipAESVerifyLen   = 2
	zipAESAuthLen     = 10
	zipPBKDF2Rounds   = 1000
)

// ---------- key derivation ----------

// pbkdf2SHA1 implements PBKDF2-HMAC-SHA1 using only the stdlib.
func pbkdf2SHA1(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha1.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	var counter [4]byte
	out := make([]byte, 0, blocks*hashLen)
	for block := 1; block <= blocks; block++ {
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		prf.Reset()
		prf.Write(salt)
		prf.Write(counter[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

type zipAESKeys struct {
	aesKey  []byte
	verify  []byte
	hmacKey []byte
	counter [aes.BlockSize]byte
}

func deriveZipAESKeys(password string, salt []byte) (zipAESKeys, error) {
	keyLen := 32 + zipAESVerifyLen + zipAESAuthLen // AES-256
	derived := pbkdf2SHA1([]byte(password), salt, zipPBKDF2Rounds, keyLen)
	k := zipAESKeys{
		aesKey:  derived[:32],
		verify:  derived[32 : 32+zipAESVerifyLen],
		hmacKey: derived[32+zipAESVerifyLen:],
	}
	// WinZip AES: 16-byte little-endian counter starting at 1 (block[0] = 1).
	k.counter[0] = 1
	return k, nil
}

func (k zipAESKeys) block() cipher.Block {
	block, err := aes.NewCipher(k.aesKey)
	if err != nil {
		panic(err) // key length is fixed at 32 bytes
	}
	return block
}

// ---------- small writer helpers ----------

type hashWriter struct {
	w io.Writer
	h hash.Hash
}

func (hw hashWriter) Write(p []byte) (int, error) {
	n, err := hw.w.Write(p)
	if n > 0 {
		_, _ = hw.h.Write(p[:n])
	}
	return n, err
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

func put16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }
func put32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
func le16(v uint16) []byte {
	b := make([]byte, 2)
	put16(b, v)
	return b
}
func le32(v uint32) []byte {
	b := make([]byte, 4)
	put32(b, v)
	return b
}

func dosDateTime(t time.Time) (uint16, uint16) {
	if t.Year() < 1980 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, t.Location())
	}
	date := uint16((t.Year()-1980)<<9) | uint16(t.Month()<<5) | uint16(t.Day())
	clock := uint16(t.Hour()<<11) | uint16(t.Minute()<<5) | uint16(t.Second()/2)
	return clock, date
}

func aesExtraField(actualMethod uint16) []byte {
	// Header ID(2) + data size(2) + version(2) + vendor(2) + strength(1) + method(2)
	field := make([]byte, 11)
	put16(field[0:2], zipAESExtraID)
	put16(field[2:4], 7)
	put16(field[4:6], zipAESVersion)
	copy(field[6:8], "AE")
	field[8] = zipAESStrength256
	put16(field[9:11], actualMethod)
	return field
}

// parseAESExtra returns (strength, actualMethod) from a WinZip 0x9901 extra field.
func parseAESExtra(extra []byte) (byte, uint16, bool) {
	for i := 0; i+4 <= len(extra); {
		id := binary.LittleEndian.Uint16(extra[i : i+2])
		size := int(binary.LittleEndian.Uint16(extra[i+2 : i+4]))
		body := extra[i+4 : i+4+size]
		if i+4+size > len(extra) {
			break
		}
		if id == zipAESExtraID && len(body) >= 7 {
			return body[4], binary.LittleEndian.Uint16(body[5:7]), true
		}
		i += 4 + size
	}
	return 0, 0, false
}

// ---------- raw ZIP writer (supports flate levels and WinZip AES-256) ----------

type zipCentralEntry struct {
	name         string
	extra        []byte
	method       uint16
	flags        uint16
	clock, date  uint16
	crc          uint32
	compressed   uint32
	uncompressed uint32
	external     uint32
	offset       uint32
}

// writeZip writes files as a ZIP archive. If password is non-empty every entry
// is encrypted with WinZip AES-256 (AE-2); otherwise level controls deflate.
func writeZip(out io.Writer, files []archives.FileInfo, level CompressionLevel, password string) error {
	if password != "" && !utf8.ValidString(password) {
		return fmt.Errorf("密码包含无效的 UTF-8 字符")
	}

	var (
		buf        bytes.Buffer // scratch for headers
		central    []zipCentralEntry
		levelValue = level.Value
	)
	if !level.Valid {
		levelValue = flate.DefaultCompression
	}

	for _, file := range files {
		name := file.NameInArchive
		if name == "" {
			name = file.Name()
		}
		if name == "" {
			continue
		}
		info := file.FileInfo
		if info.Size() > int64(^uint32(0)) {
			return fmt.Errorf("文件 %s 超过 4GB，暂不支持 ZIP64", name)
		}
		clock, date := dosDateTime(info.ModTime())

		isDir := info.IsDir()
		if isDir && len(name) > 0 && name[len(name)-1] != '/' {
			name += "/"
		}
		entry := zipCentralEntry{
			name:     name,
			method:   zipMethodDeflate,
			flags:    0x0800, // UTF-8 names
			clock:    clock,
			date:     date,
			external: uint32(info.Mode()) << 16,
			offset:   uint32(currentOffset(out)),
		}
		if isDir {
			entry.external |= 0x10
		}

		actualMethod := entry.method
		encrypted := password != "" && !isDir
		if isDir || levelValue == 0 {
			entry.method = zipMethodStore
			actualMethod = zipMethodStore
		}
		if encrypted {
			entry.method = zipMethodAES
			entry.flags |= 0x0001 // traditional encrypted bit must be set for AES too
			entry.extra = aesExtraField(actualMethod)
			entry.crc = 0 // AE-2
		}

		// local file header; sizes live in the data descriptor
		buf.Reset()
		nameBytes := []byte(name)
		buf.Write(le32(0x04034b50))
		buf.Write(le16(51)) // AES needs 5.1; harmless otherwise
		buf.Write(le16(entry.flags))
		buf.Write(le16(entry.method))
		buf.Write(le16(clock))
		buf.Write(le16(date))
		buf.Write(le32(entry.crc))
		buf.Write(le32(0))
		buf.Write(le32(0))
		buf.Write(le16(uint16(len(nameBytes))))
		buf.Write(le16(uint16(len(entry.extra))))
		buf.Write(nameBytes)
		buf.Write(entry.extra)
		if _, err := out.Write(buf.Bytes()); err != nil {
			return err
		}

		cw := &countingWriter{w: out}
		if isDir {
			entry.compressed, entry.uncompressed = 0, 0
		} else {
			var rc fs.File
			if info.Mode()&os.ModeSymlink != 0 {
				rc = &bytesFile{Reader: bytes.NewReader([]byte(file.LinkTarget))}
			} else {
				var err error
				rc, err = file.Open()
				if err != nil {
					return fmt.Errorf("读取 %s 失败：%w", name, err)
				}
			}

			if encrypted {
				salt := make([]byte, zipAESSaltLen)
				if _, err := rand.Read(salt); err != nil {
					rc.Close()
					return fmt.Errorf("生成加密盐失败：%w", err)
				}
				keys, err := deriveZipAESKeys(password, salt)
				if err != nil {
					rc.Close()
					return err
				}
				if _, err := cw.Write(salt); err != nil {
					rc.Close()
					return err
				}
				if _, err := cw.Write(keys.verify); err != nil {
					rc.Close()
					return err
				}

				mac := hmac.New(sha1.New, keys.hmacKey)
				stream := cipher.NewCTR(keys.block(), keys.counter[:])
				cipherWriter := &cipher.StreamWriter{S: stream, W: hashWriter{w: cw, h: mac}}
				if err := copyCompressed(cipherWriter, rc, actualMethod, levelValue); err != nil {
					rc.Close()
					return fmt.Errorf("压缩 %s 失败：%w", name, err)
				}
				rc.Close()
				auth := mac.Sum(nil)[:zipAESAuthLen]
				if _, err := cw.Write(auth); err != nil {
					return err
				}
				if info.Size() >= 0 {
					entry.uncompressed = uint32(info.Size())
				}
			} else {
				crcHash := crc32.NewIEEE()
				if err := copyCompressed(cw, io.TeeReader(rc, crcHash), actualMethod, levelValue); err != nil {
					rc.Close()
					return fmt.Errorf("压缩 %s 失败：%w", name, err)
				}
				rc.Close()
				entry.crc = crcHash.Sum32()
				if info.Size() >= 0 {
					entry.uncompressed = uint32(info.Size())
				}
			}
			entry.compressed = uint32(cw.n)
		}

		// data descriptor (signature + crc + sizes)
		buf.Reset()
		buf.Write(le32(0x08074b50))
		buf.Write(le32(entry.crc))
		buf.Write(le32(entry.compressed))
		buf.Write(le32(entry.uncompressed))
		if _, err := out.Write(buf.Bytes()); err != nil {
			return err
		}
		central = append(central, entry)
	}

	cdOffset := currentOffset(out)
	for _, e := range central {
		nameBytes := []byte(e.name)
		buf.Reset()
		buf.Write(le32(0x02014b50))
		buf.Write(le16(51)) // made by
		buf.Write(le16(51)) // needed
		buf.Write(le16(e.flags))
		buf.Write(le16(e.method))
		buf.Write(le16(e.clock))
		buf.Write(le16(e.date))
		buf.Write(le32(e.crc))
		buf.Write(le32(e.compressed))
		buf.Write(le32(e.uncompressed))
		buf.Write(le16(uint16(len(nameBytes))))
		buf.Write(le16(uint16(len(e.extra))))
		buf.Write(le16(0)) // comment
		buf.Write(le16(0)) // disk start
		buf.Write(le16(0)) // internal attrs
		buf.Write(le32(e.external))
		buf.Write(le32(e.offset))
		buf.Write(nameBytes)
		buf.Write(e.extra)
		if _, err := out.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	cdSize := int64(currentOffset(out)) - cdOffset

	buf.Reset()
	buf.Write(le32(0x06054b50))
	buf.Write(le16(0))
	buf.Write(le16(0))
	buf.Write(le16(uint16(len(central))))
	buf.Write(le16(uint16(len(central))))
	buf.Write(le32(uint32(cdSize)))
	buf.Write(le32(uint32(cdOffset)))
	buf.Write(le16(0))
	_, err := out.Write(buf.Bytes())
	return err
}

// currentOffset reports how many bytes an io.Writer has accepted. It supports
// *os.File, *bytes.Buffer and *countingWriter, which covers all our call sites.
func currentOffset(w io.Writer) int64 {
	switch v := w.(type) {
	case *os.File:
		if pos, err := v.Seek(0, io.SeekCurrent); err == nil {
			return pos
		}
	case *bytes.Buffer:
		return int64(v.Len())
	case *countingWriter:
		return v.n
	}
	return 0
}

func copyCompressed(dst io.Writer, src io.Reader, method uint16, level int) error {
	if method == zipMethodStore {
		_, err := copyLarge(dst, src)
		return err
	}
	fw, err := flate.NewWriter(dst, level)
	if err != nil {
		return err
	}
	if _, err := copyLarge(fw, src); err != nil {
		fw.Close()
		return err
	}
	return fw.Close()
}

type bytesFile struct {
	*bytes.Reader
}

func (bytesFile) Close() error { return nil }
func (bytesFile) Stat() (fs.FileInfo, error) {
	return nil, fmt.Errorf("stat not supported")
}

// ---------- reading AES-encrypted entries ----------

// verifyZipAESPassword checks the 2-byte verifier of the first encrypted
// entry without decrypting or reading any file payload. It makes metadata-only
// operations such as List reject a wrong password instead of returning sizes
// decrypted with the wrong key.
func verifyZipAESPassword(zf *os.File, zr *zip.Reader, password string) error {
	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		strength, _, ok := parseAESExtra(entry.Extra)
		if !ok {
			// Legacy ZipCrypto is not supported at all; walkEncryptedZip will
			// report a clearer error when the payload is opened.
			continue
		}
		saltLen := map[byte]int{1: 8, 2: 12, 3: 16}[strength]
		if saltLen == 0 {
			continue
		}
		offset, err := entry.DataOffset()
		if err != nil {
			return fmt.Errorf("读取加密 ZIP 条目位置失败：%w", err)
		}
		header := make([]byte, saltLen+zipAESVerifyLen)
		if _, err := zf.ReadAt(header, offset); err != nil {
			return fmt.Errorf("读取加密 ZIP 校验值失败：%w", err)
		}
		derived := pbkdf2SHA1([]byte(password), header[:saltLen], zipPBKDF2Rounds,
			32+zipAESVerifyLen+zipAESAuthLen)
		if subtle.ConstantTimeCompare(header[saltLen:], derived[32:32+zipAESVerifyLen]) != 1 {
			return fmt.Errorf("ZIP 密码错误")
		}
		return nil
	}
	return nil
}

// decryptZipAES decrypts and decompresses the raw bytes of a WinZip AES entry.
func decryptZipAES(raw []byte, f *zip.File, password string) ([]byte, error) {
	if len(raw) < zipAESSaltLen+zipAESVerifyLen+zipAESAuthLen {
		return nil, fmt.Errorf("加密 ZIP 条目数据过短")
	}
	strength, method, ok := parseAESExtra(f.Extra)
	if !ok {
		return nil, fmt.Errorf("加密 ZIP 条目缺少 0x9901 扩展字段")
	}
	saltLen := map[byte]int{1: 8, 2: 12, 3: 16}[strength]
	if saltLen == 0 {
		return nil, fmt.Errorf("不支持的 AES 强度：%d", strength)
	}
	keyLen := map[byte]int{1: 16, 2: 24, 3: 32}[strength]
	if len(raw) < saltLen+zipAESVerifyLen+zipAESAuthLen {
		return nil, fmt.Errorf("加密 ZIP 条目数据过短")
	}
	salt := raw[:saltLen]
	verify := raw[saltLen : saltLen+zipAESVerifyLen]
	ciphertext := raw[saltLen+zipAESVerifyLen : len(raw)-zipAESAuthLen]
	auth := raw[len(raw)-zipAESAuthLen:]

	derived := pbkdf2SHA1([]byte(password), salt, zipPBKDF2Rounds, keyLen+zipAESVerifyLen+zipAESAuthLen)
	aesKey, expectedVerify, hmacKey := derived[:keyLen], derived[keyLen:keyLen+zipAESVerifyLen], derived[keyLen+zipAESVerifyLen:]
	if subtle.ConstantTimeCompare(verify, expectedVerify) != 1 {
		return nil, fmt.Errorf("ZIP 密码错误")
	}
	mac := hmac.New(sha1.New, hmacKey)
	mac.Write(ciphertext)
	if subtle.ConstantTimeCompare(auth, mac.Sum(nil)[:zipAESAuthLen]) != 1 {
		return nil, fmt.Errorf("ZIP 条目校验失败（密码错误或数据损坏）")
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	var counter [aes.BlockSize]byte
	counter[0] = 1
	plain := make([]byte, len(ciphertext))
	cipher.NewCTR(block, counter[:]).XORKeyStream(plain, ciphertext)

	switch method {
	case zipMethodStore:
		return plain, nil
	case zipMethodDeflate:
		rc := flate.NewReader(bytes.NewReader(plain))
		defer rc.Close()
		return io.ReadAll(rc)
	default:
		return nil, fmt.Errorf("不支持的 ZIP AES 内部压缩方法：%d", method)
	}
}

// zipHasEncryption scans a zip file for AES/legacy-encrypted entries.
func zipHasEncryption(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return false, err
	}
	for _, zf := range zr.File {
		if zf.Flags&0x1 != 0 || zf.Method == zipMethodAES {
			return true, nil
		}
	}
	return false, nil
}
