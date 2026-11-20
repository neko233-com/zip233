package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentProgressIsAttributedByJobID verifies that concurrent archive
// operations keep their progress separate: each per-job callback only ever
// sees updates tagged with its own JobID, even when the operations overlap.
func TestConcurrentProgressIsAttributedByJobID(t *testing.T) {
	SetProgress(nil)
	t.Cleanup(func() { SetProgress(nil) })

	dir := t.TempDir()
	const jobs = 3
	captures := make([]*progressCapture, jobs)
	contexts := make([]context.Context, jobs)
	for i := 0; i < jobs; i++ {
		captures[i] = &progressCapture{}
		contexts[i] = WithProgress(context.Background(), fmt.Sprintf("job-%d", i), captures[i].record)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := filepath.Join(dir, fmt.Sprintf("src-%d.txt", i))
			if err := os.WriteFile(src, []byte("concurrent progress payload"), 0o644); err != nil {
				t.Error(err)
				return
			}
			target := filepath.Join(dir, fmt.Sprintf("out-%d.zip", i))
			<-start
			result, err := CreateWithContext(contexts[i], []string{src}, target, "zip", "fast", "")
			if err != nil || !result.OK {
				t.Errorf("job-%d 创建失败：result=%+v err=%v", i, result, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, capture := range captures {
		want := fmt.Sprintf("job-%d", i)
		got := capture.snapshot()
		if len(got) == 0 {
			t.Errorf("%s 未收到进度事件", want)
			continue
		}
		for _, p := range got {
			if p.JobID != want {
				t.Errorf("%s 的回调收到了 %q 的进度：%+v", want, p.JobID, p)
			}
			if p.Phase != "create" {
				t.Errorf("%s 阶段错误：%q", want, p.Phase)
			}
		}
		last := got[len(got)-1]
		if last.Total <= 0 || last.Done != last.Total {
			t.Errorf("%s 缺少准确的终态进度：%+v", want, last)
		}
	}
}

// TestTestAndExtractProgressCarryJobID covers the other two operations and
// also checks that the legacy global callback stays silent once a per-job
// callback is in effect (no duplicate or misattributed updates).
func TestTestAndExtractProgressCarryJobID(t *testing.T) {
	SetProgress(nil)
	t.Cleanup(func() { SetProgress(nil) })

	dir := t.TempDir()
	src := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(dir, "payload.zip")
	if result, err := Create([]string{src}, zipPath, "zip", "", ""); err != nil || !result.OK {
		t.Fatalf("准备测试归档失败：result=%+v err=%v", result, err)
	}

	global := &progressCapture{}
	SetProgress(global.record)
	defer SetProgress(nil)

	testCap := &progressCapture{}
	testCtx := WithProgress(context.Background(), "test-job", testCap.record)
	if result, err := TestWithContext(testCtx, zipPath, ""); err != nil || !result.OK {
		t.Fatalf("测试归档失败：result=%+v err=%v", result, err)
	}

	extractCap := &progressCapture{}
	extractCtx := WithProgress(context.Background(), "extract-job", extractCap.record)
	outDir := filepath.Join(dir, "out")
	if result, err := ExtractWithContext(extractCtx, zipPath, outDir, ""); err != nil || !result.OK {
		t.Fatalf("解压失败：result=%+v err=%v", result, err)
	}

	for _, tc := range []struct {
		name  string
		cap   *progressCapture
		jobID string
		phase string
	}{
		{"test", testCap, "test-job", "test"},
		{"extract", extractCap, "extract-job", "extract"},
	} {
		got := tc.cap.snapshot()
		if len(got) == 0 {
			t.Errorf("%s 未收到进度事件", tc.name)
			continue
		}
		for _, p := range got {
			if p.JobID != tc.jobID || p.Phase != tc.phase {
				t.Errorf("%s 进度标记错误：%+v", tc.name, p)
			}
		}
	}
	if events := global.snapshot(); len(events) != 0 {
		t.Errorf("per-job 回调生效时不应触发全局回调，实际收到 %d 条", len(events))
	}
}

// TestLegacyProgressHasEmptyJobID keeps the CLI/global path backward
// compatible: updates without WithProgress carry an empty JobID.
func TestLegacyProgressHasEmptyJobID(t *testing.T) {
	SetProgress(nil)
	t.Cleanup(func() { SetProgress(nil) })

	cap := subscribe(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "legacy.txt")
	if err := os.WriteFile(src, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := Create([]string{src}, filepath.Join(dir, "legacy.zip"), "zip", "", ""); err != nil || !result.OK {
		t.Fatalf("创建失败：result=%+v err=%v", result, err)
	}
	for _, p := range cap.snapshot() {
		if p.JobID != "" {
			t.Errorf("旧接口不应带 JobID：%+v", p)
		}
	}
}
