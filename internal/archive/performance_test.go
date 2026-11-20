package archive

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// progressCapture records progress updates from a background archive worker.
type progressCapture struct {
	mu    sync.Mutex
	items []Progress
}

func (c *progressCapture) record(p Progress) {
	c.mu.Lock()
	c.items = append(c.items, p)
	c.mu.Unlock()
}

func (c *progressCapture) snapshot() []Progress {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Progress(nil), c.items...)
}

// subscribe installs a capture and guarantees the global callback is cleared
// when the test finishes (progress reporting is process-wide state).
func subscribe(t *testing.T) *progressCapture {
	t.Helper()
	c := &progressCapture{}
	SetProgress(c.record)
	t.Cleanup(func() { SetProgress(nil) })
	return c
}

func writeLargeFixture(t testing.TB, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<16)
	for i := range buf {
		buf[i] = byte((i*131 + i/17) & 0xff)
	}
	for written := 0; written < size; {
		n := len(buf)
		if written+n > size {
			n = size - written
		}
		if _, err := f.Write(buf[:n]); err != nil {
			f.Close()
			t.Fatal(err)
		}
		written += n
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// assertProgressSequence checks ordering and the forced terminal update
// without depending on wall-clock time or an exact number of callbacks.
func assertProgressSequence(t *testing.T, got []Progress, phase string, minEntries int64) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s：没有收到进度上报", phase)
	}
	for i, p := range got {
		if p.Phase != phase {
			t.Errorf("%s：第 %d 条进度阶段错误：%q", phase, i, p.Phase)
		}
		if i > 0 && p.Done < got[i-1].Done {
			t.Errorf("%s：Done 非递增：%d -> %d", phase, got[i-1].Done, p.Done)
		}
	}
	last := got[len(got)-1]
	if last.Total < minEntries || last.Done != last.Total {
		t.Errorf("%s：收尾进度应 Done==Total 且 >= %d，实际 %+v", phase, minEntries, last)
	}
	if strings.TrimSpace(last.Message) == "" {
		t.Errorf("%s：收尾进度缺少消息：%+v", phase, last)
	}
}

// TestPerformanceRoundTrip exercises a multi-megabyte file through
// create/test/extract with progress reporting enabled, verifying correctness
// and progress ordering rather than brittle absolute time thresholds.
func TestPerformanceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	const size = 4 << 20
	src := writeLargeFixture(t, dir, "payload.bin", size)
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "out.zip")

	createProgress := subscribe(t)
	if result, err := Create([]string{src}, target, "zip", "fast", ""); err != nil || !result.OK {
		t.Fatalf("创建失败：result=%+v err=%v", result, err)
	}
	assertProgressSequence(t, createProgress.snapshot(), "create", 1)

	items, err := List(target, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("列出失败：items=%+v err=%v", items, err)
	}

	testProgress := &progressCapture{}
	SetProgress(testProgress.record)
	if result, err := Test(target, ""); err != nil || !result.OK {
		t.Fatalf("校验失败：result=%+v err=%v", result, err)
	}
	assertProgressSequence(t, testProgress.snapshot(), "test", 1)

	extractProgress := &progressCapture{}
	SetProgress(extractProgress.record)
	out := filepath.Join(dir, "unpacked")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("解压失败：result=%+v err=%v", result, err)
	}
	assertProgressSequence(t, extractProgress.snapshot(), "extract", 1)

	got, err := os.ReadFile(filepath.Join(out, "payload.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("解压内容与源文件不一致：got=%d want=%d", len(got), len(want))
	}
}

// TestNoProgressSubscriber verifies the opt-in path: with no callback the
// whole pipeline must still work and must not panic.
func TestNoProgressSubscriber(t *testing.T) {
	SetProgress(nil)
	t.Cleanup(func() { SetProgress(nil) })
	dir := t.TempDir()
	src := writeLargeFixture(t, dir, "nosub.bin", 2<<20)
	target := filepath.Join(dir, "nosub.zip")
	if result, err := Create([]string{src}, target, "zip", "fast", ""); err != nil || !result.OK {
		t.Fatalf("无订阅创建失败：result=%+v err=%v", result, err)
	}
	if result, err := Test(target, ""); err != nil || !result.OK {
		t.Fatalf("无订阅校验失败：result=%+v err=%v", result, err)
	}
	out := filepath.Join(dir, "nosub-out")
	if result, err := Extract(target, out, ""); err != nil || !result.OK {
		t.Fatalf("无订阅解压失败：result=%+v err=%v", result, err)
	}
}

// TestWrongPasswordProgress checks the failure path: a terminal progress
// update must still be delivered so the UI never shows a stuck percentage.
func TestWrongPasswordProgress(t *testing.T) {
	dir := t.TempDir()
	src := writeLargeFixture(t, dir, "secret.bin", 2<<20)
	target := filepath.Join(dir, "secret.zip")
	SetProgress(nil)
	if result, err := Create([]string{src}, target, "zip", "fast", "pw"); err != nil || !result.OK {
		t.Fatalf("创建加密包失败：result=%+v err=%v", result, err)
	}

	capture := subscribe(t)
	if result, err := Test(target, "wrong"); err != nil || result.OK {
		t.Fatalf("错误密码应校验失败：result=%+v err=%v", result, err)
	}
	got := capture.snapshot()
	if len(got) == 0 || got[len(got)-1].Message != "校验失败" {
		t.Fatalf("错误密码未上报收尾进度：%+v", got)
	}

	if result, err := Extract(target, filepath.Join(dir, "bad-out"), "wrong"); err != nil || result.OK {
		t.Fatalf("错误密码应解压失败：result=%+v err=%v", result, err)
	}
	got = capture.snapshot()
	if len(got) == 0 || got[len(got)-1].Message != "解压失败" {
		t.Fatalf("错误密码解压未上报收尾进度：%+v", got)
	}
}

func BenchmarkArchiveCreateExtract(b *testing.B) {
	SetProgress(nil)
	b.Cleanup(func() { SetProgress(nil) })
	dir := b.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		b.Fatal(err)
	}
	src := writeLargeFixture(b, srcDir, "payload.bin", 4<<20)
	target := filepath.Join(dir, "bench.zip")
	baseOut := filepath.Join(dir, "out")
	if err := os.MkdirAll(baseOut, 0o755); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if result, err := Create([]string{src}, target, "zip", "fast", ""); err != nil || !result.OK {
			b.Fatalf("创建失败：result=%+v err=%v", result, err)
		}
		out := filepath.Join(baseOut, string(rune('a'+i%26))+string(rune('a'+i/26%26)))
		if err := os.MkdirAll(out, 0o755); err != nil {
			b.Fatal(err)
		}
		if result, err := Extract(target, out, ""); err != nil || !result.OK {
			b.Fatalf("解压失败：result=%+v err=%v", result, err)
		}
	}
}
