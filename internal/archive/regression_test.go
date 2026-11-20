package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeFormatAliases(t *testing.T) {
	cases := []struct{ input, want string }{
		{"zip", "zip"},
		{" .ZIP ", "zip"},
		{"tgz", "tar.gz"},
		{".tgz", "tar.gz"},
		{"TBZ", "tar.bz2"},
		{"tbz2", "tar.bz2"},
		{"txz", "tar.xz"},
		{"taz", "tar.gz"},
		{"tar.zst", "tar.zst"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeFormat(tc.input); got != tc.want {
			t.Errorf("normalizeFormat(%q) = %q, want %q", tc.input, got, tc.want)
		}
		if tc.want == "tar.gz" || tc.want == "tar.bz2" || tc.want == "tar.xz" {
			if !IsSupportedCreateFormat(tc.input) {
				t.Errorf("别名 %q 应被识别为可创建格式", tc.input)
			}
		}
	}
}

func TestFormatFromExtension(t *testing.T) {
	cases := []struct{ target, want string }{
		{"a.zip", "zip"},
		{"a.TGZ", "tar.gz"},
		{"a.tar.bz2", "tar.bz2"},
		{"a.tar.xz", "tar.xz"},
		{"a.7z", "7z"},
		{"no-extension", ""},
	}
	for _, tc := range cases {
		if got := formatFromExtension(tc.target); got != tc.want {
			t.Errorf("formatFromExtension(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		input     string
		want      int
		wantValid bool
		wantErr   bool
	}{
		{"", -1, false, false},
		{"none", 0, true, false},
		{"store", 0, true, false},
		{"0", 0, true, false},
		{"fast", 1, true, false},
		{"1", 1, true, false},
		{"best", 9, true, false},
		{"9", 9, true, false},
		{"5", 5, true, false},
		{"10", 0, false, true},
		{"-1", 0, false, true},
		{"whatever", 0, false, true},
	}
	for _, tc := range cases {
		got, err := parseLevel(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseLevel(%q) err=%v, wantErr=%v", tc.input, err, tc.wantErr)
			continue
		}
		if got.Value != tc.want || got.Valid != tc.wantValid {
			t.Errorf("parseLevel(%q) = %+v, want value=%d valid=%v", tc.input, got, tc.want, tc.wantValid)
		}
	}
}

func TestCreateWithFormatAlias(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "alias.txt", "alias round trip")
	target := filepath.Join(dir, "aliased.tgz")

	if result, err := Create([]string{src}, target, "", "fast", ""); err != nil || !result.OK {
		t.Fatalf("按扩展名推断 tgz 失败：result=%+v err=%v", result, err)
	}
	items, err := List(target, "")
	if err != nil || len(items) != 1 || items[0].Name != "alias.txt" {
		t.Fatalf("别名归档无法列出：items=%+v err=%v", items, err)
	}
	out := filepath.Join(dir, "alias-out")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("别名归档解压失败：result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(out, "alias.txt"))
	if err != nil || string(got) != "alias round trip" {
		t.Fatalf("别名归档内容错误：%q err=%v", got, err)
	}
}

func TestCreateRejectsInvalidRequests(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "plain.txt", "plain")

	cases := []struct {
		name     string
		paths    []string
		target   string
		format   string
		level    string
		password string
		want     string
	}{
		{"empty-target", []string{src}, "", "zip", "", "", "目标路径不能为空"},
		{"unsupported-format", []string{src}, filepath.Join(dir, "x.rar"), "rar", "", "", "不支持的创建格式"},
		{"bad-level", []string{src}, filepath.Join(dir, "x.zip"), "zip", "10", "", "无效的压缩级别"},
		{"password-unsupported-format", []string{src}, filepath.Join(dir, "x.tar.gz"), "tar.gz", "", "pw", "不支持密码加密"},
		{"no-sources", nil, filepath.Join(dir, "x.zip"), "zip", "", "", "没有指定要压缩的文件"},
		{"missing-source", []string{filepath.Join(dir, "missing.txt")}, filepath.Join(dir, "x.zip"), "zip", "", "", "源路径不存在"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Create(tc.paths, tc.target, tc.format, tc.level, tc.password)
			if err != nil {
				t.Fatalf("不应返回 error：%v", err)
			}
			if result.OK {
				t.Fatal("请求非法但 result.OK=true")
			}
			if !strings.Contains(result.Stderr, tc.want) {
				t.Errorf("错误信息 %q 未包含 %q", result.Stderr, tc.want)
			}
		})
	}
}

func TestAESZipWrongPasswordOnListAndExtract(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "vault.txt", "vault payload")
	target := filepath.Join(dir, "vault.zip")
	if result, err := Create([]string{src}, target, "zip", "", "right-password"); err != nil || !result.OK {
		t.Fatalf("创建加密 zip 失败：result=%+v err=%v", result, err)
	}

	result, err := Extract(target, filepath.Join(dir, "wrong-out"), "wrong-password")
	if err == nil && result.OK {
		t.Fatal("错误密码下解压不应成功")
	}
	if _, err := List(target, "wrong-password"); err == nil {
		t.Fatal("错误密码下列出归档不应成功")
	}
	message := result.Stderr
	if err != nil {
		message += err.Error()
	}
	if message == "" {
		t.Fatal("错误密码应给出错误信息")
	}
	if data, readErr := os.ReadFile(filepath.Join(dir, "wrong-out", "vault.txt")); readErr == nil &&
		string(data) == "vault payload" {
		t.Fatal("错误密码不应解出明文")
	}
}

func TestTestRejectsCorruptArchive(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "broken.zip")
	if err := os.WriteFile(target, []byte("this is definitely not a zip archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Test(target, "")
	if err == nil && result.OK {
		t.Fatal("损坏的归档不应通过完整性检查")
	}
}

func TestSupportedFormatListsAreDefensiveCopies(t *testing.T) {
	create := SupportedCreateFormats()
	read := SupportedReadFormats()
	create[0] = "mutated"
	read[0] = "mutated"
	if SupportedCreateFormats()[0] == "mutated" || SupportedReadFormats()[0] == "mutated" {
		t.Fatal("Supported*Formats 应返回副本，避免调用方修改包内状态")
	}
}
