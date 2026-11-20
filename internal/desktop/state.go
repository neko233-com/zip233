package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const stateVersion = 1

type savedState struct {
	Version      int            `json:"version"`
	Archives     []savedArchive `json:"archives"`
	Tasks        []savedTask    `json:"tasks"`
	OutputDir    string         `json:"output_dir,omitempty"`
	SortAsc      bool           `json:"sort_asc"`
	Seq          int            `json:"seq"`
	ShellEnabled *bool          `json:"shell_enabled,omitempty"`
	SavedAt      string         `json:"saved_at,omitempty"`
}

type savedArchive struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	Name             string `json:"name"`
	Format           string `json:"format"`
	Size             int64  `json:"size"`
	Items            int    `json:"items"`
	Note             string `json:"note,omitempty"`
	PasswordRequired bool   `json:"password_required"`
	Added            string `json:"added,omitempty"`
}

type savedTask struct {
	ID      string `json:"id"`
	Kind    int    `json:"kind"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Detail  string `json:"detail,omitempty"`
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Started string `json:"started,omitempty"`
}

func statePath() string {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "zip233", "state.json")
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	stamp, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return stamp
}

func (a *App) loadState() {
	path := statePath()
	a.statePath = path
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var saved savedState
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != stateVersion {
		return
	}
	for _, item := range saved.Archives {
		if strings.TrimSpace(item.Path) == "" {
			continue
		}
		entry := ArchiveEntry{
			ID:     item.ID,
			Path:   item.Path,
			Name:   item.Name,
			Format: item.Format,
			Size:   item.Size,
			Items:  item.Items,
			Note:   item.Note,
			Added:  parseTime(item.Added),
		}
		if entry.Name == "" {
			entry.Name = filepath.Base(entry.Path)
		}
		if entry.Added.IsZero() {
			entry.Added = time.Now()
		}
		if item.PasswordRequired && entry.Note == "" {
			entry.Note = "需密码（请重新输入）"
		}
		a.archives = append(a.archives, entry)
	}
	for _, item := range saved.Tasks {
		task := TaskEntry{
			ID:      item.ID,
			Kind:    taskKind(item.Kind),
			Name:    item.Name,
			Path:    item.Path,
			Detail:  item.Detail,
			Status:  taskStatus(item.Status),
			Message: item.Message,
			Done:    item.Done,
			Total:   item.Total,
			Started: parseTime(item.Started),
		}
		if task.Status == taskRunning || task.Status == taskQueued {
			task.Status = taskFailed
			task.Message = "程序重启导致任务中断，请重新执行。"
		}
		if task.Started.IsZero() {
			task.Started = time.Now()
		}
		a.tasks = append(a.tasks, task)
	}
	if len(a.tasks) > 200 {
		a.tasks = a.tasks[:200]
	}
	a.outputDir = saved.OutputDir
	a.sortAsc = saved.SortAsc
	a.seq = saved.Seq
	// Missing field means "not configured yet": the menu stays on by default.
	if saved.ShellEnabled != nil {
		a.shellEnabled = *saved.ShellEnabled
	} else {
		a.shellEnabled = true
	}
}

// persist writes UI state atomically. It never stores archive passwords.
func (a *App) persist() {
	if a.statePath == "" {
		a.statePath = statePath()
	}
	shellEnabled := a.shellEnabled
	saved := savedState{
		Version:      stateVersion,
		SortAsc:      a.sortAsc,
		Seq:          a.seq,
		OutputDir:    a.outputDir,
		ShellEnabled: &shellEnabled,
		SavedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	for _, entry := range a.archives {
		saved.Archives = append(saved.Archives, savedArchive{
			ID:               entry.ID,
			Path:             entry.Path,
			Name:             entry.Name,
			Format:           entry.Format,
			Size:             entry.Size,
			Items:            entry.Items,
			Note:             entry.Note,
			PasswordRequired: entry.Password != "" || strings.Contains(entry.Note, "密码"),
			Added:            entry.Added.UTC().Format(time.RFC3339),
		})
	}
	for _, task := range a.tasks {
		if len(saved.Tasks) >= 200 {
			break
		}
		saved.Tasks = append(saved.Tasks, savedTask{
			ID:      task.ID,
			Kind:    int(task.Kind),
			Name:    task.Name,
			Path:    task.Path,
			Detail:  task.Detail,
			Status:  int(task.Status),
			Message: task.Message,
			Done:    task.Done,
			Total:   task.Total,
			Started: task.Started.UTC().Format(time.RFC3339),
		})
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(a.statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, "state-*.json")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmpName, a.statePath)
}

// BeforeQuit persists state before MyGo terminates the process.
func (a *App) BeforeQuit() {
	a.quitting = true
	if a.progressUnsub != nil {
		a.progressUnsub()
		a.progressUnsub = nil
	}
	if a.tray != nil {
		a.tray.Destroy()
		a.tray = nil
	}
	a.persist()
}
