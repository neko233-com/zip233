package desktop

import (
	"testing"
	"time"

	"github.com/lobehub/lobe-mygo/pkg/display"
)

func TestTaskPercent(t *testing.T) {
	cases := []struct {
		name        string
		done, total int64
		want        string
	}{
		{"no-total-no-done", 0, 0, ""},
		{"no-total-with-done", 1, 0, "1 项"},
		{"negative-total", 1, -3, "1 项"},
		{"zero", 0, 10, "0%"},
		{"half", 1, 2, "50%"},
		{"rounded-down", 1, 3, "33%"},
		{"complete", 4, 4, "100%"},
		{"capped", 9, 4, "100%"},
		{"negative-done", -1, 4, "0%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := taskPercent(TaskEntry{Done: tc.done, Total: tc.total})
			if got != tc.want {
				t.Errorf("taskPercent(%d,%d) = %q, want %q", tc.done, tc.total, got, tc.want)
			}
		})
	}
}

func TestStatusOf(t *testing.T) {
	path := "/tmp/archive.zip"
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		entry   ArchiveEntry
		tasks   []TaskEntry
		want    string
		color   display.TagColor
		running bool
	}{
		{
			name:  "creating",
			entry: ArchiveEntry{Path: path},
			tasks: []TaskEntry{{Path: path, Kind: taskCreate, Status: taskRunning,
				Done: 1, Total: 2, Started: base}},
			want: "创建中 50%", color: display.TagProcessing, running: true,
		},
		{
			name:  "extracting",
			entry: ArchiveEntry{Path: path},
			tasks: []TaskEntry{{Path: path, Kind: taskExtract, Status: taskRunning,
				Done: 3, Total: 3, Started: base}},
			want: "解压中 100%", color: display.TagProcessing, running: true,
		},
		{
			name:  "testing-without-total",
			entry: ArchiveEntry{Path: path},
			tasks: []TaskEntry{{Path: path, Kind: taskTest, Status: taskRunning, Started: base}},
			want:  "测试中 ", color: display.TagProcessing, running: true,
		},
		{
			name:  "queued",
			entry: ArchiveEntry{Path: path},
			tasks: []TaskEntry{{Path: path, Kind: taskExtract, Status: taskQueued, Started: base}},
			want:  "排队中", color: display.TagWarning,
		},
		{
			name:  "failed",
			entry: ArchiveEntry{Path: path, Items: 3},
			tasks: []TaskEntry{{Path: path, Kind: taskCreate, Status: taskFailed, Started: base}},
			want:  "失败", color: display.TagError,
		},
		{
			name:  "done",
			entry: ArchiveEntry{Path: path},
			tasks: []TaskEntry{{Path: path, Kind: taskExtract, Status: taskDone, Started: base}},
			want:  "完成", color: display.TagSuccess,
		},
		{
			name: "ready", entry: ArchiveEntry{Path: path, Items: 5},
			want: "已就绪", color: display.TagSuccess,
		},
		{
			name: "password-required", entry: ArchiveEntry{Path: path, Note: "需要密码"},
			want: "需密码", color: display.TagWarning,
		},
		{
			name: "unread", entry: ArchiveEntry{Path: path},
			want: "未读取", color: display.TagGray,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := &App{archives: []ArchiveEntry{tc.entry}, tasks: tc.tasks}
			label, color, running := app.statusOf(tc.entry)
			if label != tc.want || color != tc.color || running != tc.running {
				t.Errorf("statusOf() = (%q, %v, %v), want (%q, %v, %v)",
					label, color, running, tc.want, tc.color, tc.running)
			}
		})
	}
}

func TestStatusOfUsesLatestTask(t *testing.T) {
	path := "/tmp/archive.zip"
	// 新任务总是前置到切片头部，latestTask 取第一个匹配项。
	app := &App{
		archives: []ArchiveEntry{{Path: path}},
		tasks: []TaskEntry{
			{Path: path, Kind: taskCreate, Status: taskDone},
			{Path: path, Kind: taskCreate, Status: taskFailed},
		},
	}
	label, _, running := app.statusOf(app.archives[0])
	if label != "完成" || running {
		t.Errorf("应取最近的任务状态：label=%q running=%v", label, running)
	}
}

func TestPureHelpers(t *testing.T) {
	if got := formatFromPath("/tmp/a.tar.gz"); got != "tar.gz" {
		t.Errorf("formatFromPath tar.gz = %q", got)
	}
	if got := formatFromPath("/tmp/a.TGZ"); got != "tgz" {
		t.Errorf("formatFromPath tgz = %q", got)
	}
	if got := formatFromPath("/tmp/noext"); got != "unknown" {
		t.Errorf("formatFromPath noext = %q", got)
	}
	if got := humanBytes(0); got != "—" {
		t.Errorf("humanBytes(0) = %q", got)
	}
	if got := humanBytes(2048); got != "2.0 KB" {
		t.Errorf("humanBytes(2048) = %q", got)
	}
}
