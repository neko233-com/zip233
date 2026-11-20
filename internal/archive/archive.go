package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mholt/archives"
	"github.com/neko233-com/zip233/internal/contract"
	"github.com/unxed/sevenzip"
)

// entryOpener lazily opens the content of one archive entry. It is nil for
// directories and for metadata-only walks.
type entryOpener func() (io.ReadCloser, error)

// io.Copy's default 32KiB buffer costs many extra read/write syscalls on the
// multi-megabyte files this tool is built for. A pooled 1MiB buffer keeps the
// copy path saturated without allocating per entry. io.CopyBuffer still uses
// the optimized WriterTo/ReaderFrom path when the source/destination provides
// one, so small entries are not penalized.
const copyBufferSize = 1 << 20

var copyBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, copyBufferSize)
		return &buf
	},
}

func copyLarge(dst io.Writer, src io.Reader) (int64, error) {
	bufp := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufp)
	return io.CopyBuffer(dst, src, *bufp)
}

// walkCallback receives one archive entry. Implementations must not retain the
// opener beyond the callback (the underlying archive stream may be advanced).
type walkCallback func(info archives.FileInfo, open entryOpener) error

// walkEntries identifies the format of path and streams every entry to handle.
// With readData=false no entry content is opened (used by List).
func walkEntries(path, password string, readData bool, handle walkCallback) error {
	ctx := context.Background()
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("无法打开压缩包：%w", err)
	}
	defer f.Close()

	format, _, err := archives.Identify(ctx, filepath.Base(path), f)
	if err != nil {
		return fmt.Errorf("无法识别压缩包格式：%w", err)
	}

	// Passwords: 7z and rar extractors carry a Password field.
	switch v := format.(type) {
	case archives.SevenZip:
		v.Password = password
		format = v
	case archives.Rar:
		v.Password = password
		format = v
	case archives.CompressedArchive:
		switch c := v.Extraction.(type) {
		case archives.SevenZip:
			c.Password = password
			v.Extraction = c
		case archives.Rar:
			c.Password = password
			v.Extraction = c
		}
		format = v
	}

	// ZIP needs special handling because mholt/archives cannot decrypt it.
	if _, isZip := format.(archives.Zip); isZip {
		encrypted, err := zipHasEncryption(path)
		if err != nil {
			return fmt.Errorf("读取 ZIP 失败：%w", err)
		}
		if encrypted {
			if password == "" {
				return fmt.Errorf("该 ZIP 已加密，请提供密码")
			}
			return walkEncryptedZip(path, password, readData, handle)
		}
		if password != "" {
			return fmt.Errorf("该 ZIP 未加密，无需提供密码")
		}
	}

	// CompressedArchive embeds Compression, so exclude it before the
	// standalone-stream branch (tar.gz is an archive, not a single file).
	if _, isCompressedArchive := format.(archives.CompressedArchive); !isCompressedArchive {
		if comp, ok := format.(archives.Compression); ok {
			return walkSingleCompressed(f, comp, path, readData, handle)
		}
	}

	ex, ok := format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("不支持读取该格式：%T", format)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return ex.Extract(ctx, f, func(_ context.Context, fi archives.FileInfo) error {
		var opener entryOpener
		if readData && !fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
			opener = func() (io.ReadCloser, error) { return fi.Open() }
		}
		return handle(fi, opener)
	})
}

// walkEncryptedZip walks a WinZip AES encrypted ZIP using the stdlib reader
// for the central directory and our own AE-2 decoder for entry data.
func walkEncryptedZip(path, password string, readData bool, handle walkCallback) error {
	zf, err := os.Open(path)
	if err != nil {
		return err
	}
	defer zf.Close()
	stat, err := zf.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(zf, stat.Size())
	if err != nil {
		return fmt.Errorf("读取 ZIP 目录失败：%w", err)
	}
	if password != "" && !readData {
		if err := verifyZipAESPassword(zf, zr, password); err != nil {
			return err
		}
	}
	for _, zfEntry := range zr.File {
		var opener entryOpener
		if readData && !zfEntry.FileInfo().IsDir() {
			entry := zfEntry
			opener = func() (io.ReadCloser, error) {
				offset, err := entry.DataOffset()
				if err != nil {
					return nil, err
				}
				raw := make([]byte, entry.CompressedSize64)
				if _, err := zf.ReadAt(raw, offset); err != nil {
					return nil, err
				}
				plain, err := decryptZipAES(raw, entry, password)
				if err != nil {
					return nil, err
				}
				return &memFile{Reader: bytes.NewReader(plain), info: entry.FileInfo()}, nil
			}
		}
		info := archives.FileInfo{
			FileInfo:      zfEntry.FileInfo(),
			NameInArchive: zfEntry.Name,
			Open: func() (fs.File, error) {
				if opener == nil {
					return nil, fmt.Errorf("条目不可读取")
				}
				rc, err := opener()
				if err != nil {
					return nil, err
				}
				return &rcFile{ReadCloser: rc, info: zfEntry.FileInfo()}, nil
			},
		}
		if err := handle(info, opener); err != nil {
			return err
		}
	}
	return nil
}

type memFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (m *memFile) Close() error               { return nil }
func (m *memFile) Stat() (fs.FileInfo, error) { return m.info, nil }

type rcFile struct {
	io.ReadCloser
	info fs.FileInfo
}

func (r *rcFile) Stat() (fs.FileInfo, error) { return r.info, nil }

// walkSingleCompressed treats a plain gz/bz2/xz/zst file as one virtual entry.
func walkSingleCompressed(f *os.File, comp archives.Compression, path string, readData bool, handle walkCallback) error {
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	for _, ext := range []string{".gz", ".bz2", ".xz", ".zst", ".zstd"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	info := syntheticInfo{name: name, size: stat.Size(), mode: 0o644, mod: stat.ModTime()}
	var opener entryOpener
	if readData {
		opener = func() (io.ReadCloser, error) {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
			return comp.OpenReader(f)
		}
	}
	return handle(archives.FileInfo{
		FileInfo:      info,
		NameInArchive: name,
		Open: func() (fs.File, error) {
			if opener == nil {
				return nil, fmt.Errorf("条目不可读取")
			}
			rc, err := opener()
			if err != nil {
				return nil, err
			}
			return &rcFile{ReadCloser: rc, info: info}, nil
		},
	}, opener)
}

type syntheticInfo struct {
	name string
	size int64
	mode fs.FileMode
	mod  time.Time
}

func (s syntheticInfo) Name() string       { return s.name }
func (s syntheticInfo) Size() int64        { return s.size }
func (s syntheticInfo) Mode() fs.FileMode  { return s.mode }
func (s syntheticInfo) ModTime() time.Time { return s.mod }
func (s syntheticInfo) IsDir() bool        { return false }
func (s syntheticInfo) Sys() any           { return nil }

// packedSize best-effort extracts a compressed size from a format header.
func packedSize(header any) *int64 {
	if header == nil {
		return nil
	}
	v := reflect.ValueOf(header)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	field := v.FieldByName("CompressedSize64")
	if !field.IsValid() || !field.CanInterface() {
		return nil
	}
	switch n := field.Interface().(type) {
	case uint64:
		x := int64(n)
		return &x
	case int64:
		x := n
		return &x
	case uint32:
		x := int64(n)
		return &x
	}
	return nil
}

func modifiedString(info fs.FileInfo) *string {
	if info == nil {
		return nil
	}
	s := info.ModTime().UTC().Format(time.RFC3339)
	return &s
}

// List returns archive entries without reading their contents.
func List(path, password string) ([]contract.ArchiveItem, error) {
	items := make([]contract.ArchiveItem, 0)
	err := walkEntries(path, password, false, func(fi archives.FileInfo, _ entryOpener) error {
		size := fi.Size()
		item := contract.ArchiveItem{
			Name:        fi.NameInArchive,
			Size:        &size,
			IsDirectory: fi.IsDir(),
			Modified:    modifiedString(fi.FileInfo),
			PackedSize:  packedSize(fi.Header),
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func failResult(err error) contract.ToolResult {
	return contract.ToolResult{OK: false, Stderr: err.Error()}
}

func okResult(message string) contract.ToolResult {
	return contract.ToolResult{OK: true, Stdout: message}
}

// Test reads every entry end-to-end to verify integrity and the password.
// The entry count is learned from the same walk (no separate List pass), so
// the archive is read exactly once even when a progress callback is active.
func Test(path, password string) (contract.ToolResult, error) {
	return TestWithContext(context.Background(), path, password)
}

// TestWithContext is Test with a per-operation progress context. A callback
// registered by WithProgress receives every update tagged with its JobID,
// isolated from any global callback and from concurrent operations.
func TestWithContext(ctx context.Context, path, password string) (contract.ToolResult, error) {
	emitter := newProgressEmitter(ctx, "test", 0)
	count := 0
	err := walkEntries(path, password, true, func(fi archives.FileInfo, open entryOpener) error {
		if fi.IsDir() {
			return nil
		}
		if open == nil {
			return fmt.Errorf("条目 %s 无法读取", fi.NameInArchive)
		}
		rc, err := open()
		if err != nil {
			return fmt.Errorf("打开 %s 失败：%w", fi.NameInArchive, err)
		}
		_, err = copyLarge(io.Discard, rc)
		closeErr := rc.Close()
		if err != nil {
			return fmt.Errorf("读取 %s 失败：%w", fi.NameInArchive, err)
		}
		if closeErr != nil {
			return fmt.Errorf("校验 %s 失败：%w", fi.NameInArchive, closeErr)
		}
		count++
		emitter.report(int64(count), fi.NameInArchive, false)
		return nil
	})
	if err != nil {
		emitter.finish(int64(count), "校验失败")
		return failResult(err), nil
	}
	emitter.finish(int64(count), "校验完成")
	return okResult(fmt.Sprintf("测试通过：%d 个条目", count)), nil
}

// safeJoin resolves a (possibly hostile) archive entry name under base.
func safeJoin(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("压缩包包含空条目名")
	}
	if strings.ContainsRune(name, 0) || strings.ContainsRune(name, ':') {
		return "", fmt.Errorf("条目名包含非法字符：%q", name)
	}
	n := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(n, "//") || strings.HasPrefix(n, "\\\\") {
		return "", fmt.Errorf("拒绝 UNC 路径：%q", name)
	}
	if pathpkg.IsAbs(n) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("拒绝绝对路径或盘符：%q", name)
	}
	for _, part := range strings.Split(n, "/") {
		if part == ".." {
			return "", fmt.Errorf("拒绝路径穿越：%q", name)
		}
	}
	clean := pathpkg.Clean(n)
	if clean == "." || clean == "/" {
		clean = ""
	}
	target := base
	if clean != "" {
		target = filepath.Join(base, filepath.FromSlash(clean))
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("条目路径越界：%q", name)
	}
	return target, nil
}

// ensureNoSymlinkParents refuses to write through a pre-existing symlink.
func ensureNoSymlinkParents(base, target string) error {
	rel, err := filepath.Rel(base, filepath.Dir(target))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("目标路径越界：%q", target)
	}
	cur := base
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝通过符号链接写入：%s", cur)
		}
	}
	return nil
}

// safeSymlinkTarget validates a symlink entry stays inside outputDir.
func safeSymlinkTarget(base, target, link string) error {
	if link == "" {
		return fmt.Errorf("符号链接目标为空：%q", target)
	}
	l := strings.ReplaceAll(link, "\\", "/")
	if pathpkg.IsAbs(l) || filepath.VolumeName(link) != "" || strings.HasPrefix(l, "//") {
		return fmt.Errorf("拒绝绝对符号链接：%q", link)
	}
	resolved := pathpkg.Clean(pathpkg.Join(pathpkg.Dir(filepath.ToSlash(target)), l))
	abs := filepath.Join(base, filepath.FromSlash(resolved))
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("符号链接指向目标目录之外：%q", link)
	}
	return nil
}

// Extract unpacks an archive into outputDir after strict path validation.
func Extract(path, outputDir, password string) (contract.ToolResult, error) {
	return ExtractWithContext(context.Background(), path, outputDir, password)
}

// ExtractWithContext is Extract with a per-operation progress context.
func ExtractWithContext(ctx context.Context, path, outputDir, password string) (contract.ToolResult, error) {
	base, err := filepath.Abs(outputDir)
	if err != nil {
		return failResult(fmt.Errorf("无效的输出目录：%w", err)), nil
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return failResult(fmt.Errorf("创建输出目录失败：%w", err)), nil
	}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}

	// The total is discovered during this single walk instead of a separate
	// List pass; a forced terminal update publishes the accurate count.
	emitter := newProgressEmitter(ctx, "extract", 0)
	count := 0
	advance := func(name string) {
		count++
		emitter.report(int64(count), name, false)
	}
	err = walkEntries(path, password, true, func(fi archives.FileInfo, open entryOpener) error {
		target, err := safeJoin(base, fi.NameInArchive)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if err := ensureNoSymlinkParents(base, target); err != nil {
				return err
			}
			if err := safeSymlinkTarget(base, target, fi.LinkTarget); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(fi.LinkTarget, target); err != nil {
				return fmt.Errorf("创建符号链接 %s 失败：%w", fi.NameInArchive, err)
			}
			advance(fi.NameInArchive)
			return nil
		}
		if fi.IsDir() {
			if err := ensureNoSymlinkParents(base, target); err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("创建目录 %s 失败：%w", fi.NameInArchive, err)
			}
			advance(fi.NameInArchive)
			return nil
		}
		if open == nil {
			return fmt.Errorf("条目 %s 无法读取", fi.NameInArchive)
		}
		if err := ensureNoSymlinkParents(base, target); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("创建目录失败：%w", err)
		}
		rc, err := open()
		if err != nil {
			return fmt.Errorf("打开 %s 失败：%w", fi.NameInArchive, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
		if err != nil {
			rc.Close()
			return fmt.Errorf("创建文件 %s 失败：%w", fi.NameInArchive, err)
		}
		_, copyErr := copyLarge(out, rc)
		// Close each entry's handles immediately; deferring inside the walk
		// callback would keep every reader alive until Extract returns.
		closeErr := rc.Close()
		closeOutErr := out.Close()
		if copyErr != nil {
			return fmt.Errorf("写入 %s 失败：%w", fi.NameInArchive, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("读取 %s 失败：%w", fi.NameInArchive, closeErr)
		}
		if closeOutErr != nil {
			return fmt.Errorf("关闭 %s 失败：%w", fi.NameInArchive, closeOutErr)
		}
		advance(fi.NameInArchive)
		return nil
	})
	if err != nil {
		emitter.finish(int64(count), "解压失败")
		return failResult(err), nil
	}
	emitter.finish(int64(count), "解压完成")
	return okResult(fmt.Sprintf("解压完成：%d 个条目 → %s", count, base)), nil
}

// buildFileInfos gathers files/directories from disk into archive.FileInfos.
func buildFileInfos(paths []string) ([]archives.FileInfo, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("没有指定要压缩的文件")
	}
	diskToName := make(map[string]string, len(paths))
	used := make(map[string]bool, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("无效路径 %q：%w", p, err)
		}
		if _, err := os.Lstat(abs); err != nil {
			return nil, fmt.Errorf("源路径不存在：%s", p)
		}
		name := filepath.ToSlash(filepath.Base(filepath.Clean(abs)))
		if name == "." || name == "" || name == "/" {
			name = "archive"
		}
		candidate := name
		for i := 2; used[candidate]; i++ {
			candidate = fmt.Sprintf("%s_%d%s", name, i, filepath.Ext(name))
		}
		used[candidate] = true
		diskToName[abs] = candidate
	}
	files, err := archives.FilesFromDisk(context.Background(), &archives.FromDiskOptions{FollowSymlinks: false}, diskToName)
	if err != nil {
		return nil, fmt.Errorf("收集文件失败：%w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("没有可压缩的内容")
	}
	return files, nil
}

// instrumentCreateFiles wraps each source FileInfo's Open so the CLI can show
// per-entry progress while the archive writer consumes the files. Writers
// iterate files in order, so an index-based Done stays monotonic. When no
// callback is registered the original slice is returned untouched.
func instrumentCreateFiles(files []archives.FileInfo, emitter *progressEmitter) []archives.FileInfo {
	if !emitter.active() {
		return files
	}
	wrapped := make([]archives.FileInfo, len(files))
	copy(wrapped, files)
	for i := range wrapped {
		idx := i
		origOpen := wrapped[i].Open
		name := wrapped[i].NameInArchive
		wrapped[i].Open = func() (fs.File, error) {
			emitter.report(int64(idx+1), name, false)
			if origOpen == nil {
				return nil, fmt.Errorf("条目 %s 不可读取", name)
			}
			return origOpen()
		}
	}
	emitter.report(0, "准备压缩", true)
	return wrapped
}

// Create builds a new archive at target from paths.
func Create(paths []string, target, format, level, password string) (contract.ToolResult, error) {
	return CreateWithContext(context.Background(), paths, target, format, level, password)
}

// CreateWithContext is Create with a per-operation progress context.
func CreateWithContext(ctx context.Context, paths []string, target, format, level, password string) (contract.ToolResult, error) {
	if strings.TrimSpace(target) == "" {
		return failResult(fmt.Errorf("目标路径不能为空")), nil
	}
	name := normalizeFormat(format)
	if name == "" {
		name = formatFromExtension(target)
	}
	if !IsSupportedCreateFormat(name) {
		return failResult(fmt.Errorf("不支持的创建格式 %q；支持：%s", format, strings.Join(SupportedCreateFormats(), ", "))), nil
	}
	lvl, err := parseLevel(level)
	if err != nil {
		return failResult(err), nil
	}
	if password != "" {
		switch name {
		case "zip", "7z":
		default:
			return failResult(fmt.Errorf("格式 %s 不支持密码加密；请使用 zip 或 7z", name)), nil
		}
	}

	files, err := buildFileInfos(paths)
	if err != nil {
		return failResult(err), nil
	}
	total := int64(len(files))
	emitter := newProgressEmitter(ctx, "create", total)
	files = instrumentCreateFiles(files, emitter)

	if name == "7z" {
		if err := create7z(files, target, lvl, password); err != nil {
			return failResult(err), nil
		}
		emitter.report(total, "已创建 7z", true)
		return okResult(fmt.Sprintf("已创建 7z：%s", target)), nil
	}

	if name == "gz" || name == "bz2" || name == "xz" {
		if err := createStream(target, name, paths, lvl); err != nil {
			return failResult(err), nil
		}
		emitter.report(total, "压缩完成", true)
		return okResult(fmt.Sprintf("已创建 %s：%s", name, target)), nil
	}

	out, err := os.Create(target)
	if err != nil {
		return failResult(fmt.Errorf("创建目标文件失败：%w", err)), nil
	}
	committed := false
	defer func() {
		out.Close()
		if !committed {
			_ = os.Remove(target)
		}
	}()

	switch name {
	case "zip":
		err = writeZip(out, files, lvl, password)
	case "tar":
		err = archives.Tar{}.Archive(ctx, out, files)
	case "tar.gz":
		err = (archives.CompressedArchive{Archival: archives.Tar{}, Extraction: archives.Tar{}, Compression: archives.Gz{CompressionLevel: flateLevel(lvl)}}).Archive(ctx, out, files)
	case "tar.bz2":
		err = (archives.CompressedArchive{Archival: archives.Tar{}, Extraction: archives.Tar{}, Compression: archives.Bz2{CompressionLevel: bzip2Level(lvl)}}).Archive(ctx, out, files)
	case "tar.xz":
		err = (archives.CompressedArchive{Archival: archives.Tar{}, Extraction: archives.Tar{}, Compression: archives.Xz{}}).Archive(ctx, out, files)
	default:
		err = fmt.Errorf("尚未实现的格式：%s", name)
	}
	if err != nil {
		return failResult(fmt.Errorf("压缩失败：%w", err)), nil
	}
	if err := out.Sync(); err != nil {
		return failResult(fmt.Errorf("写入目标文件失败：%w", err)), nil
	}
	committed = true
	emitter.report(total, "压缩完成", true)
	return okResult(fmt.Sprintf("已创建 %s：%s（%d 个源文件）", name, target, len(paths))), nil
}

func flateLevel(lvl CompressionLevel) int {
	if !lvl.Valid {
		return -1 // library default
	}
	return lvl.Value
}

func bzip2Level(lvl CompressionLevel) int {
	if !lvl.Valid {
		return -1
	}
	if lvl.Value == 0 {
		return 0
	}
	return lvl.Value
}

// createStream handles single-file gz/bz2/xz creation (no archive layer).
func createStream(target, name string, paths []string, lvl CompressionLevel) error {
	if len(paths) != 1 {
		return fmt.Errorf("%s 格式一次只能压缩一个文件", name)
	}
	src, err := os.Open(paths[0])
	if err != nil {
		return fmt.Errorf("打开源文件失败：%w", err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s 格式不支持压缩目录", name)
	}
	out, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("创建目标文件失败：%w", err)
	}
	committed := false
	defer func() {
		out.Close()
		if !committed {
			_ = os.Remove(target)
		}
	}()

	var wc io.WriteCloser
	switch name {
	case "gz":
		wc, err = archives.Gz{CompressionLevel: flateLevel(lvl)}.OpenWriter(out)
	case "bz2":
		wc, err = archives.Bz2{CompressionLevel: bzip2Level(lvl)}.OpenWriter(out)
	case "xz":
		wc, err = archives.Xz{}.OpenWriter(out)
	}
	if err != nil {
		return fmt.Errorf("初始化压缩失败：%w", err)
	}
	if _, err := copyLarge(wc, src); err != nil {
		wc.Close()
		return fmt.Errorf("压缩失败：%w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("结束压缩失败：%w", err)
	}
	if err := out.Sync(); err != nil {
		return err
	}
	committed = true
	return nil
}

// create7z writes a 7z archive using the pure-Go unxed/sevenzip writer. It
// consumes the same []archives.FileInfo that the other formats use (already
// instrumented for progress), so no external 7-Zip CLI is required.
func create7z(files []archives.FileInfo, target string, lvl CompressionLevel, password string) error {
	// Write to a temporary file first so a failed run never destroys an
	// existing archive at target.
	tmp := target + ".zip233-tmp.7z"
	_ = os.Remove(tmp)
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("创建目标文件失败：%w", err)
	}
	committed := false
	defer func() {
		_ = out.Close()
		if !committed {
			_ = os.Remove(tmp)
		}
	}()

	// unxed/sevenzip always compresses with LZMA2 and exposes no explicit
	// level/dictionary option. Map the user level onto the knobs it does
	// expose: "none/store" produces independent per-file streams (fast random
	// access), while every other level uses solid compression. Concurrency is
	// only honored by the library for non-solid archives.
	solid := !lvl.Valid || lvl.Value != 0
	concurrency := 1
	if solid {
		concurrency = runtime.NumCPU()
		if concurrency < 1 {
			concurrency = 1
		}
	}
	opts := []sevenzip.WriterOption{
		sevenzip.WithSolid(solid),
		sevenzip.WithConcurrency(concurrency),
	}
	if password != "" {
		opts = append(opts, sevenzip.WithPassword(password))
	}

	w, err := sevenzip.NewWriter(out, opts...)
	if err != nil {
		return fmt.Errorf("初始化 7z 写入器失败：%w", err)
	}

	for _, fi := range files {
		if err := write7zEntry(w, fi); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("写入 7z 目录失败：%w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("写入目标文件失败：%w", err)
	}
	// Windows cannot rename an open file, so close before committing.
	if err := out.Close(); err != nil {
		return fmt.Errorf("关闭目标文件失败：%w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存 7z 失败：%w", err)
	}
	committed = true
	return nil
}

// write7zEntry writes one FileInfo (file or directory) into a 7z writer.
func write7zEntry(w *sevenzip.Writer, fi archives.FileInfo) error {
	name := strings.ReplaceAll(fi.NameInArchive, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return nil
	}

	header := &sevenzip.FileHeader{
		Name:       name,
		Modified:   fi.ModTime(),
		Attributes: sevenzipAttributes(fi.Mode()),
	}
	if fi.IsDir() {
		if !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}
		fw, err := w.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("写入目录 %s 失败：%w", name, err)
		}
		if err := fw.Close(); err != nil {
			return fmt.Errorf("关闭目录 %s 失败：%w", name, err)
		}
		return nil
	}

	fw, err := w.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("写入条目 %s 失败：%w", name, err)
	}
	// Symlinks are stored as regular files containing the link target; the
	// 7z reader used by zip233 has no symlink support, so marking them as
	// symlinks would break extraction (empty LinkTarget).
	var src io.ReadCloser
	if fi.LinkTarget != "" {
		src = io.NopCloser(strings.NewReader(fi.LinkTarget))
	} else {
		src, err = fi.Open()
		if err != nil {
			return fmt.Errorf("打开 %s 失败：%w", name, err)
		}
		defer src.Close()
	}
	if _, err := copyLarge(fw, src); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	if err := fw.Close(); err != nil {
		return fmt.Errorf("关闭条目 %s 失败：%w", name, err)
	}
	return nil
}

// sevenzipAttributes converts an fs.FileMode into the POSIX-style attribute
// word expected by the 7z format (and understood by both the writer and the
// bodgit reader). Symlinks are deliberately stored as regular files.
func sevenzipAttributes(mode fs.FileMode) uint32 {
	var m uint32
	if mode.IsDir() {
		m = 0x4000 // sIFDIR
	} else {
		m = 0x8000 // sIFREG
	}
	m |= uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		m |= 0x800
	}
	if mode&os.ModeSetgid != 0 {
		m |= 0x400
	}
	if mode&os.ModeSticky != 0 {
		m |= 0x200
	}
	return m << 16
}

// DirectCompressZip silently creates a ZIP at an already-reserved target path.
// It never creates or truncates the target; the caller owns that step.
func DirectCompressZip(sources []string, target string) error {
	files, err := buildFileInfos(sources)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("打开目标文件失败：%w", err)
	}
	defer out.Close()
	return writeZip(out, files, CompressionLevel{}, "")
}
