package archive

import (
	"archive/zip"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试文件失败：%v", err)
	}
	return p
}

func TestZipRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "hello.txt", "hello zip233")
	target := filepath.Join(dir, "out.zip")

	if result, err := Create([]string{src}, target, "zip", "", ""); err != nil || !result.OK {
		t.Fatalf("创建 zip 失败：result=%+v err=%v", result, err)
	}

	items, err := List(target, "")
	if err != nil {
		t.Fatalf("列出 zip 失败：%v", err)
	}
	if len(items) != 1 || items[0].Name != "hello.txt" {
		t.Fatalf("意外的条目：%+v", items)
	}
	if items[0].Size == nil || *items[0].Size != int64(len("hello zip233")) {
		t.Fatalf("条目大小不正确：%+v", items[0])
	}

	out := filepath.Join(dir, "unzip")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("解压 zip 失败：result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(out, "hello.txt"))
	if err != nil || string(got) != "hello zip233" {
		t.Fatalf("解压内容不匹配：%q err=%v", got, err)
	}

	if result, err := Test(target, ""); err != nil || !result.OK {
		t.Fatalf("测试 zip 失败：result=%+v err=%v", result, err)
	}
}

func TestTarGzRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "note.txt", "tar.gz content")
	target := filepath.Join(dir, "out.tar.gz")

	if result, err := Create([]string{src}, target, "tar.gz", "best", ""); err != nil || !result.OK {
		t.Fatalf("创建 tar.gz 失败：result=%+v err=%v", result, err)
	}
	items, err := List(target, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("列出 tar.gz 失败：items=%+v err=%v", items, err)
	}
	out := filepath.Join(dir, "untar")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("解压 tar.gz 失败：result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(out, "note.txt"))
	if err != nil || string(got) != "tar.gz content" {
		t.Fatalf("解压内容不匹配：%q err=%v", got, err)
	}
}

func TestAESZipRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "secret.txt", "top secret payload")
	target := filepath.Join(dir, "secret.zip")

	if result, err := Create([]string{src}, target, "zip", "", "correct horse"); err != nil || !result.OK {
		t.Fatalf("创建加密 zip 失败：result=%+v err=%v", result, err)
	}
	encrypted, err := zipHasEncryption(target)
	if err != nil || !encrypted {
		t.Fatalf("zip 未标记为加密：encrypted=%v err=%v", encrypted, err)
	}
	if _, err := List(target, ""); err == nil {
		t.Fatal("未提供密码时 List 应当失败")
	}
	items, err := List(target, "correct horse")
	if err != nil || len(items) != 1 {
		t.Fatalf("列出加密 zip 失败：items=%+v err=%v", items, err)
	}
	out := filepath.Join(dir, "dec")
	if result, err := Extract(target, out, "correct horse"); err != nil || !result.OK {
		t.Fatalf("解压加密 zip 失败：result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(out, "secret.txt"))
	if err != nil || string(got) != "top secret payload" {
		t.Fatalf("解密内容不匹配：%q err=%v", got, err)
	}
	if result, err := Test(target, "wrong password"); err != nil || result.OK {
		t.Fatalf("错误密码应当校验失败：result=%+v err=%v", result, err)
	}
}

// TestPBKDF2SHA1Vectors uses RFC 6070 known-answer vectors, proving the key
// derivation matches the WinZip AE-2 specification (SHA-1, not SHA-256).
func TestPBKDF2SHA1Vectors(t *testing.T) {
	cases := []struct {
		password, salt string
		iter, keyLen   int
		want           string
	}{
		{"password", "salt", 1, 20, "0c60c80f961f0e71f3a9b524af6012062fe037a6"},
		{"password", "salt", 2, 20, "ea6c014dc72d6f8ccd1ed92ace1d41f0d8de8957"},
		{"password", "salt", 4096, 20, "4b007901b765489abead49d926f721d065a429c1"},
	}
	for _, tc := range cases {
		got := hex.EncodeToString(pbkdf2SHA1([]byte(tc.password), []byte(tc.salt), tc.iter, tc.keyLen))
		if got != tc.want {
			t.Errorf("PBKDF2(%q,%q,%d)=%s，期望 %s", tc.password, tc.salt, tc.iter, got, tc.want)
		}
	}
}

// TestAESZipWireFormat checks the on-disk layout against the WinZip AE-2 spec.
func TestAESZipWireFormat(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "payload.txt", "wire format check")
	target := filepath.Join(dir, "wire.zip")
	if result, err := Create([]string{src}, target, "zip", "", "pw"); err != nil || !result.OK {
		t.Fatalf("创建失败：result=%+v err=%v", result, err)
	}
	f, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()
	zr, err := zip.NewReader(f, stat.Size())
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("条目数错误：%d", len(zr.File))
	}
	entry := zr.File[0]
	if entry.Method != zipMethodAES {
		t.Fatalf("压缩方法应为 99，实际 %d", entry.Method)
	}
	if entry.Flags&0x1 == 0 {
		t.Fatal("未设置加密标志位")
	}
	if entry.CRC32 != 0 {
		t.Fatalf("AE-2 的 CRC 必须为 0，实际 %d", entry.CRC32)
	}
	strength, method, ok := parseAESExtra(entry.Extra)
	if !ok || strength != 3 || method != zipMethodDeflate {
		t.Fatalf("0x9901 扩展字段错误：strength=%d method=%d ok=%v extra=%x", strength, method, ok, entry.Extra)
	}
	if entry.CompressedSize64 < uint64(zipAESSaltLen+zipAESVerifyLen+zipAESAuthLen) {
		t.Fatalf("密文长度不包含盐/校验值/HMAC：%d", entry.CompressedSize64)
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "evil.zip")
	f, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	names := []string{
		"../escape.txt",
		"/absolute.txt",
		"sub/../../escape2.txt",
		"windows\\..\\..\\escape3.txt",
		"C:/windows-escape.txt",
	}
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("pwned")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := filepath.Join(dir, "out")
	result, err := Extract(target, out, "")
	if err != nil || result.OK {
		t.Fatalf("路径穿越应被拒绝：result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Stderr, "拒绝") && !strings.Contains(result.Stderr, "越界") && !strings.Contains(result.Stderr, "绝对路径") {
		t.Fatalf("错误信息不够明确：%q", result.Stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("危险文件被写到了输出目录之外")
	}
}

func TestListBasicFields(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "data.bin", "12345")
	target := filepath.Join(dir, "list.zip")
	if result, err := Create([]string{src}, target, "zip", "fast", ""); err != nil || !result.OK {
		t.Fatalf("创建失败：result=%+v err=%v", result, err)
	}
	items, err := List(target, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("List 失败：items=%+v err=%v", items, err)
	}
	item := items[0]
	if item.Name != "data.bin" || item.IsDirectory {
		t.Fatalf("名称或类型错误：%+v", item)
	}
	if item.Size == nil || *item.Size != 5 {
		t.Fatalf("Size 错误：%+v", item)
	}
	if item.Modified == nil || *item.Modified == "" {
		t.Fatalf("Modified 缺失：%+v", item)
	}
}

func TestDirectCompressZip(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "direct.txt", "direct content")
	target := filepath.Join(dir, "direct.zip")
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := DirectCompressZip([]string{src}, target); err != nil {
		t.Fatalf("静默压缩失败：%v", err)
	}
	items, err := List(target, "")
	if err != nil || len(items) != 1 || items[0].Name != "direct.txt" {
		t.Fatalf("静默压缩结果错误：items=%+v err=%v", items, err)
	}
}
