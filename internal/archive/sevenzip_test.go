package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSevenZipRoundTrip covers passwordless 7z creation: the archive must be
// readable by the existing List/Test/Extract pipeline.
func TestSevenZipRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "hello.txt", "hello 7z233")
	target := filepath.Join(dir, "out.7z")

	if result, err := Create([]string{src}, target, "7z", "", ""); err != nil || !result.OK {
		t.Fatalf("创建 7z 失败：result=%+v err=%v", result, err)
	}
	items, err := List(target, "")
	if err != nil || len(items) != 1 || items[0].Name != "hello.txt" {
		t.Fatalf("列出 7z 失败：items=%+v err=%v", items, err)
	}
	if items[0].Size == nil || *items[0].Size != int64(len("hello 7z233")) {
		t.Fatalf("条目大小不正确：%+v", items[0])
	}

	out := filepath.Join(dir, "un7z")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("解压 7z 失败：result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(out, "hello.txt"))
	if err != nil || string(got) != "hello 7z233" {
		t.Fatalf("解压内容不匹配：%q err=%v", got, err)
	}

	if result, err := Test(target, ""); err != nil || !result.OK {
		t.Fatalf("校验 7z 失败：result=%+v err=%v", result, err)
	}
}

// TestSevenZipEncryptedRoundTrip covers AES-encrypted (including encrypted
// header) 7z creation and the wrong-password failure path.
func TestSevenZipEncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "secret.txt", "top secret 7z payload")
	target := filepath.Join(dir, "secret.7z")
	const password = "correct horse"

	if result, err := Create([]string{src}, target, "7z", "best", password); err != nil || !result.OK {
		t.Fatalf("创建加密 7z 失败：result=%+v err=%v", result, err)
	}
	if _, err := List(target, ""); err == nil {
		t.Fatal("未提供密码时 List 应当失败")
	}
	items, err := List(target, password)
	if err != nil || len(items) != 1 || items[0].Name != "secret.txt" {
		t.Fatalf("列出加密 7z 失败：items=%+v err=%v", items, err)
	}

	out := filepath.Join(dir, "dec")
	if result, err := Extract(target, out, password); err != nil || !result.OK {
		t.Fatalf("解压加密 7z 失败：result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(out, "secret.txt"))
	if err != nil || string(got) != "top secret 7z payload" {
		t.Fatalf("解密内容不匹配：%q err=%v", got, err)
	}
	if result, err := Test(target, password); err != nil || !result.OK {
		t.Fatalf("校验加密 7z 失败：result=%+v err=%v", result, err)
	}

	if result, err := Test(target, "wrong password"); err != nil || result.OK {
		t.Fatalf("错误密码应当校验失败：result=%+v err=%v", result, err)
	}
	if result, err := Extract(target, filepath.Join(dir, "bad"), "wrong password"); err != nil || result.OK {
		t.Fatalf("错误密码应当解压失败：result=%+v err=%v", result, err)
	}
}

// TestSevenZipDirectoriesAndEmptyFile verifies that directory hierarchy and
// empty files survive a create/List/Test/Extract round trip.
func TestSevenZipDirectoriesAndEmptyFile(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "sub", "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "tree.7z")

	if result, err := Create([]string{tree}, target, "7z", "", ""); err != nil || !result.OK {
		t.Fatalf("创建 7z 失败：result=%+v err=%v", result, err)
	}

	items, err := List(target, "")
	if err != nil {
		t.Fatalf("列出 7z 失败：%v", err)
	}
	dirs := 0
	for _, item := range items {
		if item.IsDirectory {
			dirs++
		}
	}
	if dirs < 2 {
		t.Fatalf("目录条目不足：%+v", items)
	}

	out := filepath.Join(dir, "out")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("解压 7z 失败：result=%+v err=%v", result, err)
	}
	wants := map[string]string{
		"tree/a.txt":     "alpha",
		"tree/sub/b.txt": "beta",
		"tree/empty.txt": "",
	}
	for rel, want := range wants {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("文件 %s 内容不匹配：%q err=%v", rel, got, err)
		}
	}
	if info, err := os.Stat(filepath.Join(out, "tree", "sub")); err != nil || !info.IsDir() {
		t.Fatalf("目录未正确重建：err=%v", err)
	}

	if result, err := Test(target, ""); err != nil || !result.OK {
		t.Fatalf("校验 7z 失败：result=%+v err=%v", result, err)
	}
}

// TestSevenZipStoreLevel exercises the non-solid (level "none") path.
func TestSevenZipStoreLevel(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "data.bin", strings.Repeat("x", 4096))
	target := filepath.Join(dir, "store.7z")

	if result, err := Create([]string{src}, target, "7z", "none", ""); err != nil || !result.OK {
		t.Fatalf("创建 7z (store) 失败：result=%+v err=%v", result, err)
	}
	if result, err := Test(target, ""); err != nil || !result.OK {
		t.Fatalf("校验 7z (store) 失败：result=%+v err=%v", result, err)
	}
	out := filepath.Join(dir, "out")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("解压 7z (store) 失败：result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(out, "data.bin"))
	if err != nil || string(got) != strings.Repeat("x", 4096) {
		t.Fatalf("解压内容不匹配：%q err=%v", got, err)
	}
}
