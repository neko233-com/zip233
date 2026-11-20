package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateStateDir redirects os.UserConfigDir into a temp directory so neither
// persist nor loadState ever touches the real user config directory.
func isolateStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return dir
}

func configDirFromEnv(t *testing.T) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("读取用户配置目录失败：%v", err)
	}
	return dir
}

func statePathFromEnv(t *testing.T) string {
	t.Helper()
	return filepath.Join(configDirFromEnv(t), "zip233", "state.json")
}

func TestStatePersistRoundTrip(t *testing.T) {
	dir := isolateStateDir(t)
	path := statePathFromEnv(t)

	added := time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)
	started := time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC)
	a := &App{
		statePath: path,
		sortAsc:   true,
		seq:       7,
		outputDir: filepath.Join(dir, "downloads"),
		archives: []ArchiveEntry{
			{
				ID: "arch-1", Path: filepath.Join(dir, "a.zip"), Name: "a.zip",
				Format: "zip", Size: 1234, Items: 3, Password: "super-secret", Added: added,
			},
			{
				ID: "arch-2", Path: filepath.Join(dir, "b.7z"), Name: "b.7z",
				Format: "7z", Size: 99, Items: 1, Note: "需密码（请重新输入）", Added: added,
			},
		},
		tasks: []TaskEntry{
			{
				ID: "task-1", Kind: taskCreate, Name: "a.zip", Path: filepath.Join(dir, "a.zip"),
				Status: taskRunning, Message: "正在创建", Done: 1, Total: 2, Started: started,
			},
			{
				ID: "task-2", Kind: taskExtract, Name: "b.7z", Path: filepath.Join(dir, "b.7z"),
				Status: taskDone, Message: "解压完成", Done: 4, Total: 4, Started: started,
			},
		},
	}
	a.persist()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("状态文件未写入临时目录：%v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("状态文件应以换行结尾")
	}
	if strings.Contains(string(data), "super-secret") {
		t.Fatal("密码绝不能被写入状态文件")
	}
	var raw savedState
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Version != stateVersion || raw.Seq != 7 || !raw.SortAsc || raw.OutputDir != a.outputDir {
		t.Fatalf("持久化字段错误：%+v", raw)
	}
	if raw.SavedAt == "" {
		t.Error("SavedAt 缺失")
	}
	if len(raw.Archives) != 2 || len(raw.Tasks) != 2 {
		t.Fatalf("归档/任务数量错误：%d/%d", len(raw.Archives), len(raw.Tasks))
	}
	if !raw.Archives[0].PasswordRequired {
		t.Error("带密码的归档应标记 PasswordRequired")
	}

	// 原子写入不应留下临时文件。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "state-") {
			t.Errorf("残留临时文件：%s", entry.Name())
		}
	}

	var b App
	b.loadState()
	if b.statePath != path {
		t.Errorf("loadState 使用了错误的路径：%s", b.statePath)
	}
	if b.seq != 7 || !b.sortAsc || b.outputDir != a.outputDir {
		t.Errorf("标量字段往返失败：seq=%d sortAsc=%v outputDir=%q", b.seq, b.sortAsc, b.outputDir)
	}
	if len(b.archives) != 2 || len(b.tasks) != 2 {
		t.Fatalf("往返后数量错误：archives=%d tasks=%d", len(b.archives), len(b.tasks))
	}
	if b.archives[0].ID != "arch-1" || b.archives[0].Name != "a.zip" || b.archives[0].Size != 1234 {
		t.Errorf("归档字段往返失败：%+v", b.archives[0])
	}
	if !b.archives[0].Added.Equal(added) {
		t.Errorf("Added 往返失败：%v", b.archives[0].Added)
	}
	if b.archives[0].Password != "" {
		t.Error("密码不应被持久化后再加载")
	}
	if b.archives[0].Note != "需密码（请重新输入）" {
		t.Errorf("密码提醒未还原：%q", b.archives[0].Note)
	}
	// 重启后运行中的任务必须标记为失败，避免界面永远显示进度。
	if b.tasks[0].Status != taskFailed || !strings.Contains(b.tasks[0].Message, "重启") {
		t.Errorf("运行中的任务应转为失败：%+v", b.tasks[0])
	}
	if b.tasks[1].Status != taskDone || b.tasks[1].Message != "解压完成" {
		t.Errorf("已完成的任务应原样还原：%+v", b.tasks[1])
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("状态目录应位于临时目录：%v", err)
	}
}

func TestStateQueuedTaskFailsOnRestart(t *testing.T) {
	isolateStateDir(t)
	path := statePathFromEnv(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := savedState{
		Version: stateVersion,
		Tasks: []savedTask{
			{ID: "queued", Status: int(taskQueued), Name: "a.zip"},
			{ID: "running", Status: int(taskRunning), Name: "b.zip"},
			{ID: "done", Status: int(taskDone), Name: "c.zip"},
		},
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var app App
	app.loadState()
	if len(app.tasks) != 3 {
		t.Fatalf("任务数量错误：%d", len(app.tasks))
	}
	for i, want := range []taskStatus{taskFailed, taskFailed, taskDone} {
		if app.tasks[i].Status != want {
			t.Errorf("任务 %s 状态错误：got=%v want=%v", app.tasks[i].ID, app.tasks[i].Status, want)
		}
	}
}

func TestStateIgnoresInvalidFiles(t *testing.T) {
	isolateStateDir(t)
	path := statePathFromEnv(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, content string
	}{
		{"corrupt", "{not json"},
		{"wrong-version", `{"version":99,"archives":[{"path":"x.zip"}]}`},
		{"empty-path", `{"version":1,"archives":[{"path":"  ","name":"ignored"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			var app App
			app.loadState()
			if len(app.archives) != 0 || len(app.tasks) != 0 {
				t.Fatalf("无效状态不应被加载：archives=%d tasks=%d", len(app.archives), len(app.tasks))
			}
		})
	}
}

func TestStateCapsTasks(t *testing.T) {
	dir := isolateStateDir(t)
	path := statePathFromEnv(t)

	var saved savedState
	saved.Version = stateVersion
	for i := 0; i < 205; i++ {
		saved.Tasks = append(saved.Tasks, savedTask{ID: string(rune('a' + i%26)), Status: int(taskDone)})
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var app App
	app.loadState()
	if len(app.tasks) != 200 {
		t.Fatalf("任务列表应截断到 200，实际 %d", len(app.tasks))
	}

	// persist 同样限制 200 条。
	out := filepath.Join(dir, "copy", "state.json")
	app.statePath = out
	app.tasks = append(app.tasks, TaskEntry{ID: "extra", Status: taskDone})
	app.persist()
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var check savedState
	if err := json.Unmarshal(written, &check); err != nil {
		t.Fatal(err)
	}
	if len(check.Tasks) != 200 {
		t.Fatalf("persist 应只写 200 条，实际 %d", len(check.Tasks))
	}
}
