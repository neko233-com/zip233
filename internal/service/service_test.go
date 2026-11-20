package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/neko233-com/zip233/internal/archive"
	"github.com/neko233-com/zip233/internal/contract"
)

// newProgressService builds a Service without New(): New() spawns background
// update/registry goroutines, which would make tests depend on network and the
// Windows registry. Only the progress fan-out wiring is reproduced here; the
// subscribe/unsubscribe logic under test is the real Service method.
func newProgressService(t *testing.T) *Service {
	t.Helper()
	s := &Service{progressSubs: make(map[int]func(contract.ArchiveProgress))}
	archive.SetProgress(func(p archive.Progress) {
		s.progressMu.RLock()
		callbacks := make([]func(contract.ArchiveProgress), 0, len(s.progressSubs))
		for _, fn := range s.progressSubs {
			callbacks = append(callbacks, fn)
		}
		s.progressMu.RUnlock()
		converted := convertProgress(p)
		for _, fn := range callbacks {
			fn(converted)
		}
	})
	t.Cleanup(func() { archive.SetProgress(nil) })
	return s
}

func emitCreateProgress(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(src, []byte("progress payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "payload.zip")
	if result, err := archive.Create([]string{src}, target, "zip", "fast", ""); err != nil || !result.OK {
		t.Fatalf("创建进度测试归档失败：result=%+v err=%v", result, err)
	}
}

func TestLaunchRequest(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })

	cases := []struct {
		name string
		args []string
		want contract.LaunchRequest
	}{
		{"no-args", []string{"zip233"}, contract.LaunchRequest{Action: ""}},
		{"action-only", []string{"zip233", "compress-zip"}, contract.LaunchRequest{Action: "compress-zip"}},
		{"action-and-path", []string{"zip233", "open", `C:\tmp\a.zip`}, contract.LaunchRequest{
			Action: "open", Path: ptr(`C:\tmp\a.zip`),
		}},
		{"empty-path", []string{"zip233", "open", ""}, contract.LaunchRequest{
			Action: "open", Path: ptr(""),
		}},
		{"extra-args-ignored", []string{"zip233", "open", "a.zip", "ignored"}, contract.LaunchRequest{
			Action: "open", Path: ptr("a.zip"),
		}},
	}
	s := &Service{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = append([]string(nil), tc.args...)
			got := s.LaunchRequest()
			if got.Action != tc.want.Action {
				t.Errorf("Action = %q, want %q", got.Action, tc.want.Action)
			}
			if (got.Path == nil) != (tc.want.Path == nil) {
				t.Fatalf("Path 空值不匹配：got=%v want=%v", got.Path, tc.want.Path)
			}
			if got.Path != nil && *got.Path != *tc.want.Path {
				t.Errorf("Path = %q, want %q", *got.Path, *tc.want.Path)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestRunShellActionRejectsInvalidInput(t *testing.T) {
	s := &Service{}
	dir := t.TempDir()
	file := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(file, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	cases := []struct {
		name   string
		action string
		path   string
		want   string
	}{
		{"unknown-action", "no-such-action", dir, "未知"},
		{"empty-action", "", dir, "未知"},
		{"compress-missing-dir", "compress-zip", missing, "无法访问文件夹"},
		{"compress-file-as-dir", "compress-zip", file, "不是文件夹"},
		{"compress-here-missing-dir", "compress-zip-here", missing, "无法访问文件夹"},
		{"extract-missing-file", "extract-here", missing, "无法访问归档"},
		{"extract-directory", "extract-here", dir, "是文件夹"},
		{"extract-to-missing-file", "extract-to", missing, "无法访问归档"},
		{"test-missing-file", "test-archive", missing, "无法访问归档"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.runShellAction(tc.action, tc.path)
			if err == nil {
				t.Fatal("应当返回错误")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误 %q 未包含 %q", err, tc.want)
			}
		})
	}
}

func TestRunShellActionCompressZip(t *testing.T) {
	s := &Service{}
	parent := t.TempDir()
	dir := filepath.Join(parent, "folder")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.runShellAction("compress-zip", dir); err != nil {
		t.Fatalf("compress-zip 失败：%v", err)
	}
	target := filepath.Join(parent, "folder.zip")
	items, err := archive.List(target, "")
	if err != nil || len(items) < 1 {
		t.Fatalf("生成的 ZIP 无法列出：items=%+v err=%v", items, err)
	}
	found := false
	for _, item := range items {
		if item.Name == "folder/a.txt" || item.Name == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("生成的 ZIP 缺少源文件：items=%+v", items)
	}

	// reserveZip must pick the next free name instead of overwriting.
	if err := s.runShellAction("compress-zip", dir); err != nil {
		t.Fatalf("第二次 compress-zip 失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "folder (2).zip")); err != nil {
		t.Fatalf("未生成 folder (2).zip：%v", err)
	}
}

func TestRunShellActionCompressZipHere(t *testing.T) {
	s := &Service{}
	dir := t.TempDir()
	if err := s.runShellAction("compress-zip-here", dir); err == nil ||
		!strings.Contains(err.Error(), "空") {
		t.Fatalf("空文件夹应报错，实际 err=%v", err)
	}

	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.runShellAction("compress-zip-here", dir); err != nil {
		t.Fatalf("compress-zip-here 失败：%v", err)
	}
	items, err := archive.List(filepath.Join(dir, filepath.Base(dir)+".zip"), "")
	if err != nil || len(items) != 2 {
		t.Fatalf("生成的 ZIP 内容错误：items=%+v err=%v", items, err)
	}
}

func TestRunShellActionExtractHere(t *testing.T) {
	s := &Service{}
	dir := t.TempDir()
	src := filepath.Join(dir, "inside.txt")
	if err := os.WriteFile(src, []byte("extracted by shell action"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "bundle.zip")
	if result, err := archive.Create([]string{src}, target, "zip", "", ""); err != nil || !result.OK {
		t.Fatalf("创建测试归档失败：result=%+v err=%v", result, err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	if err := s.runShellAction("extract-here", target); err != nil {
		t.Fatalf("extract-here 失败：%v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "inside.txt"))
	if err != nil || string(got) != "extracted by shell action" {
		t.Fatalf("解压内容不匹配：%q err=%v", got, err)
	}
}

func TestRunShellActionExtractFolder(t *testing.T) {
	s := &Service{}
	dir := t.TempDir()
	src := filepath.Join(dir, "inside.txt")
	if err := os.WriteFile(src, []byte("same-name folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "bundle.zip")
	if result, err := archive.Create([]string{src}, target, "zip", "", ""); err != nil || !result.OK {
		t.Fatalf("创建测试归档失败：result=%+v err=%v", result, err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	if err := s.runShellAction("extract-folder", target); err != nil {
		t.Fatalf("extract-folder 失败：%v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "bundle", "inside.txt"))
	if err != nil || string(got) != "same-name folder" {
		t.Fatalf("同名目录解压内容不匹配：%q err=%v", got, err)
	}

	// 再次解压不能合并进已有目录，应生成 "bundle (2)"。
	if err := s.runShellAction("extract-folder", target); err != nil {
		t.Fatalf("第二次 extract-folder 失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundle (2)", "inside.txt")); err != nil {
		t.Fatalf("重复解压应创建带序号的新目录：%v", err)
	}
}

func TestRunShellActionEncryptedArchiveHint(t *testing.T) {
	s := &Service{}
	dir := t.TempDir()
	src := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(src, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "secret.zip")
	if result, err := archive.Create([]string{src}, target, "zip", "", "pw"); err != nil || !result.OK {
		t.Fatalf("创建加密归档失败：result=%+v err=%v", result, err)
	}

	// extract-here 走 passwordHint，失败时必须给出可操作的密码提示。
	err := s.runShellAction("extract-here", target)
	if err == nil || !strings.Contains(err.Error(), "密码") {
		t.Fatalf("加密归档应给出密码提示，实际 err=%v", err)
	}
}

func TestSubscribeArchiveProgressFanoutAndUnsubscribe(t *testing.T) {
	s := newProgressService(t)

	var mu sync.Mutex
	var aEvents, bEvents []contract.ArchiveProgress
	unsubA := s.SubscribeArchiveProgress(func(p contract.ArchiveProgress) {
		mu.Lock()
		aEvents = append(aEvents, p)
		mu.Unlock()
	})
	unsubB := s.SubscribeArchiveProgress(func(p contract.ArchiveProgress) {
		mu.Lock()
		bEvents = append(bEvents, p)
		mu.Unlock()
	})

	emitCreateProgress(t)

	mu.Lock()
	aCount, bCount := len(aEvents), len(bEvents)
	for _, p := range bEvents {
		if p.Phase != "create" || p.Total <= 0 || p.JobID != "" {
			t.Errorf("进度事件字段错误：%+v", p)
		}
	}
	mu.Unlock()
	if aCount == 0 || bCount == 0 || aCount != bCount {
		t.Fatalf("两个订阅者都应收到相同数量的事件：a=%d b=%d", aCount, bCount)
	}

	unsubA()
	emitCreateProgress(t)

	mu.Lock()
	aAfter, bAfter := len(aEvents), len(bEvents)
	mu.Unlock()
	if aAfter != aCount {
		t.Errorf("退订后仍收到事件：%d -> %d", aCount, aAfter)
	}
	if bAfter <= bCount {
		t.Errorf("未退订的订阅者应继续收到事件：%d -> %d", bCount, bAfter)
	}
	unsubB()

	// 重复退订必须是安全的空操作。
	unsubA()
	unsubB()
	emitCreateProgress(t)
}

func TestSubscribeArchiveProgressUniqueIDs(t *testing.T) {
	s := &Service{progressSubs: make(map[int]func(contract.ArchiveProgress))}
	noop := func(contract.ArchiveProgress) {}
	a := s.SubscribeArchiveProgress(noop)
	b := s.SubscribeArchiveProgress(noop)
	defer a()
	defer b()

	s.progressMu.RLock()
	count := len(s.progressSubs)
	s.progressMu.RUnlock()
	if count != 2 {
		t.Fatalf("期望 2 个订阅者，实际 %d", count)
	}
	a()
	s.progressMu.RLock()
	count = len(s.progressSubs)
	_, stillB := s.progressSubs[2]
	s.progressMu.RUnlock()
	if count != 1 || !stillB {
		t.Fatalf("退订 A 后应只剩 B：count=%d stillB=%v", count, stillB)
	}
}

func TestSetAutoCheckRoundTrip(t *testing.T) {
	// os.UserConfigDir 在 Windows 读 AppData，在 Unix 读 XDG_CONFIG_HOME/HOME。
	// 全部指向临时目录，确保不触碰真实用户配置。
	temp := t.TempDir()
	t.Setenv("APPDATA", temp)
	t.Setenv("LOCALAPPDATA", temp)
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("HOME", temp)

	s := &Service{}
	original := s.AutoCheckEnabled()
	t.Cleanup(func() { s.SetAutoCheck(original) })

	s.SetAutoCheck(true)
	if !s.AutoCheckEnabled() {
		t.Fatal("SetAutoCheck(true) 后 AutoCheckEnabled 应为 true")
	}
	s.SetAutoCheck(false)
	if s.AutoCheckEnabled() {
		t.Fatal("SetAutoCheck(false) 后 AutoCheckEnabled 应为 false")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("读取用户配置目录失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "zip233", "update.json")); err != nil {
		t.Fatalf("配置应写入临时目录：%v", err)
	}
}

// TestJobAwareProgressFanout verifies that every subscriber sees the JobID
// produced by concurrent job-aware operations, and that all three WithJob
// methods (create/test/extract) carry their job id through the fan-out.
func TestJobAwareProgressFanout(t *testing.T) {
	s := newProgressService(t)

	var mu sync.Mutex
	var events []contract.ArchiveProgress
	unsub := s.SubscribeArchiveProgress(func(p contract.ArchiveProgress) {
		mu.Lock()
		events = append(events, p)
		mu.Unlock()
	})
	defer unsub()

	dir := t.TempDir()
	jobs := []string{"job-A", "job-B"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, jobID := range jobs {
		wg.Add(1)
		go func(i int, jobID string) {
			defer wg.Done()
			src := filepath.Join(dir, fmt.Sprintf("src-%d.txt", i))
			if err := os.WriteFile(src, []byte("job payload"), 0o644); err != nil {
				t.Error(err)
				return
			}
			target := filepath.Join(dir, fmt.Sprintf("out-%d.zip", i))
			<-start
			result, err := s.CreateArchiveWithJob(jobID, []string{src}, target, "zip", "fast", "")
			if err != nil || !result.OK {
				t.Errorf("%s 创建失败：result=%+v err=%v", jobID, result, err)
			}
		}(i, jobID)
	}
	close(start)
	wg.Wait()

	mu.Lock()
	seen := make(map[string]int)
	for _, p := range events {
		if p.Phase != "create" || p.Total <= 0 {
			t.Errorf("进度事件字段错误：%+v", p)
		}
		seen[p.JobID]++
	}
	events = nil
	mu.Unlock()
	for _, jobID := range jobs {
		if seen[jobID] == 0 {
			t.Errorf("job %s 未收到进度事件", jobID)
		}
	}
	if len(seen) != len(jobs) {
		t.Errorf("进度 JobID 集合错误：%+v", seen)
	}

	// Test/Extract 走同一条 jobContext 通道，确认 JobID 未被丢失。
	zipPath := filepath.Join(dir, "bundle.zip")
	if result, err := s.CreateArchive([]string{filepath.Join(dir, "src-0.txt")}, zipPath, "zip", "", ""); err != nil || !result.OK {
		t.Fatalf("准备归档失败：result=%+v err=%v", result, err)
	}
	if result, err := s.TestArchiveWithJob("test-job", zipPath, ""); err != nil || !result.OK {
		t.Fatalf("测试失败：result=%+v err=%v", result, err)
	}
	if result, err := s.ExtractArchiveWithJob("extract-job", zipPath, filepath.Join(dir, "out"), ""); err != nil || !result.OK {
		t.Fatalf("解压失败：result=%+v err=%v", result, err)
	}

	mu.Lock()
	defer mu.Unlock()
	seen = make(map[string]int)
	for _, p := range events {
		seen[p.JobID]++
	}
	if seen["test-job"] == 0 || seen["extract-job"] == 0 {
		t.Errorf("Test/Extract 的 JobID 未透传：%+v", seen)
	}
}
