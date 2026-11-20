package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// testRoot mirrors main()'s command tree without calling os.Exit.
func testRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "zip233-cli",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "")
	root.PersistentFlags().IntVar(&flagJobs, "jobs", 0, "")
	root.AddCommand(formatsCmd(), listCmd(), testCmd(), extractCmd(), createCmd(), benchmarkCmd(), splitCmd(), joinCmd())
	return root
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var fnErr error
	func() {
		defer func() {
			os.Stdout = original
			_ = w.Close()
		}()
		fnErr = fn()
	}()
	data, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), fnErr
}

func executeJSON(t *testing.T, args ...string) (string, error) {
	t.Helper()
	originalJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = originalJSON })
	jsonOutput = true

	root := testRoot()
	root.SetArgs(args)
	return captureStdout(t, root.Execute)
}

func TestFormatsJSON(t *testing.T) {
	out, err := executeJSON(t, "formats", "--json")
	if err != nil {
		t.Fatalf("formats --json 失败：%v", err)
	}
	var payload struct {
		Create []string `json:"create"`
		Read   []string `json:"read"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("输出不是合法 JSON：%q err=%v", out, err)
	}
	for _, want := range []string{"zip", "7z", "tar.gz"} {
		if !contains(payload.Create, want) {
			t.Errorf("create 缺少 %s：%v", want, payload.Create)
		}
		if !contains(payload.Read, want) {
			t.Errorf("read 缺少 %s：%v", want, payload.Read)
		}
	}
}

func TestBenchJSON(t *testing.T) {
	out, err := executeJSON(t, "bench", "--formats", "zip", "--json")
	if err != nil {
		t.Fatalf("bench --json 失败：%v", err)
	}
	var payload struct {
		Rows []benchRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("输出不是合法 JSON：%q err=%v", out, err)
	}
	if len(payload.Rows) != 1 {
		t.Fatalf("期望 1 行基准结果，实际 %d", len(payload.Rows))
	}
	row := payload.Rows[0]
	if row.Format != "zip" || !row.OK || row.Original <= 0 || row.Archive <= 0 {
		t.Fatalf("基准结果异常：%+v", row)
	}
}

func TestBenchJSONParallelOrder(t *testing.T) {
	// Row order must follow the input --formats list even though the formats
	// are benchmarked concurrently. --jobs exercises the semaphore path.
	want := []string{"tar.gz", "zip", "7z"}
	out, err := executeJSON(t, "bench", "--formats", strings.Join(want, ","), "--jobs", "2", "--json")
	if err != nil {
		t.Fatalf("bench --json 失败：%v", err)
	}
	var payload struct {
		Rows []benchRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("输出不是合法 JSON：%q err=%v", out, err)
	}
	if len(payload.Rows) != len(want) {
		t.Fatalf("期望 %d 行基准结果，实际 %d", len(want), len(payload.Rows))
	}
	for i, format := range want {
		if payload.Rows[i].Format != format {
			t.Errorf("第 %d 行格式 = %q，期望 %q", i, payload.Rows[i].Format, format)
		}
	}
}

func TestSplitJoinRoundTripViaCommands(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "payload.bin")
	content := strings.Repeat("zip233-split-roundtrip-", 32)
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := executeJSON(t, "split", source, "--size", "16", "--json")
	if err != nil {
		t.Fatalf("split --json 失败：%v", err)
	}
	var split splitResult
	if err := json.Unmarshal([]byte(out), &split); err != nil {
		t.Fatalf("split 输出错误：%q err=%v", out, err)
	}
	if split.Parts < 2 || split.Volume != 16 || split.Manifest == "" {
		t.Fatalf("分卷结果异常：%+v", split)
	}
	first := filepath.Clean(source) + ".part-001"
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("第一分卷不存在：%v", err)
	}

	joined := filepath.Join(dir, "joined.bin")
	out, err = executeJSON(t, "join", first, "--output", joined, "--json")
	if err != nil {
		t.Fatalf("join --json 失败：%v", err)
	}
	var result joinResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("join 输出错误：%q err=%v", out, err)
	}
	if result.Output != joined || result.Size != int64(len(content)) {
		t.Fatalf("合并结果异常：%+v", result)
	}
	got, err := os.ReadFile(joined)
	if err != nil || string(got) != content {
		t.Fatalf("合并内容不匹配：len=%d err=%v", len(got), err)
	}
}

func TestCLIErrorPaths(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.zip")
	originalJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = originalJSON })
	jsonOutput = true

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"extract-requires-output", []string{"extract", missing, "--json"}, "--output"},
		{"list-missing-archive", []string{"list", missing, "--json"}, "无法"},
		{"split-zero-size", []string{"split", source, "--size", "0", "--json"}, "无法识别分卷大小"},
		{"split-bad-size", []string{"split", source, "--size", "abc", "--json"}, "无法识别分卷大小"},
		{"join-missing-manifest", []string{"join", source + ".part-001", "--json"}, "找不到分卷清单"},
		{"unknown-flag", []string{"formats", "--no-such-flag", "--json"}, "unknown flag"},
		{"missing-required-arg", []string{"list", "--json"}, "accepts 1 arg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := testRoot()
			root.SetArgs(tc.args)
			errOut, err := captureStdout(t, root.Execute)
			if err == nil {
				t.Fatalf("应当返回错误，stdout=%q", errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误 %q 未包含 %q", err, tc.want)
			}
		})
	}
}

func TestPureCLIHelpers(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
	}{
		{"10K", 10 << 10}, {"5M", 5 << 20}, {"1G", 1 << 30}, {"512", 512},
	} {
		got, err := parseSize(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", tc.input, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "0", "-1", "10T", "abc"} {
		if got, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) 应失败，得到 %d", bad, got)
		}
	}
	got := splitCSV(" zip ,7z,, tar.gz ")
	if len(got) != 3 || got[0] != "zip" || got[2] != "tar.gz" {
		t.Errorf("splitCSV = %v", got)
	}
	lines := formatItems(nil)
	if len(lines) != 1 || !strings.Contains(lines[0], "0") {
		t.Errorf("formatItems(nil) = %v", lines)
	}
	if mbps(1024*1024, 1000) < 0.9 {
		t.Error("mbps 计算错误")
	}
	if humanBytes(512) != "512 B" || humanBytes(2048) != "2.0 KB" {
		t.Error("humanBytes 计算错误")
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
