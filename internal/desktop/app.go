// Package desktop 提供 zip233 的 MyGo 原生 GPU 界面。
//
// 它只通过 contract.Service 与归档/更新/文件对话框子系统交互，不直接依赖
// internal/archive，也不启动本地 HTTP 服务或 webview。
package desktop

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/feedback"

	"github.com/neko233-com/zip233/internal/contract"
	"github.com/neko233-com/zip233/internal/version"
)

//go:embed assets/logo.png
var logoPNG []byte

//go:embed assets/tray.png
var trayPNG []byte

// LogoPNG returns the embedded application logo bytes (window icon).
func LogoPNG() []byte { return logoPNG }

type page int

const (
	pageWorkbench page = iota
	pageSettings
	pageAbout
)

type taskKind int

const (
	taskCreate taskKind = iota
	taskExtract
	taskTest
)

type taskStatus int

const (
	taskRunning taskStatus = iota
	taskDone
	taskFailed
	taskQueued // waiting for a free concurrency slot
)

// ArchiveEntry is one archive shown in the workbench table.
type ArchiveEntry struct {
	ID       string
	Path     string
	Name     string
	Format   string
	Size     int64
	Items    int
	Password string
	Note     string
	Added    time.Time
}

// TaskEntry is one create/extract/test operation.
type TaskEntry struct {
	ID      string
	Kind    taskKind
	Name    string
	Path    string
	Detail  string
	Status  taskStatus
	Message string
	Done    int64
	Total   int64
	Started time.Time
}

// App holds the window-independent state behind ui.View(app.View).
type App struct {
	svc       contract.Service
	win       *mygo.Window
	tray      *mygo.Tray
	logo      *ui.Bitmap
	quitting  bool
	closeTray bool

	page     page
	filter   string
	search   string
	selected string
	sortAsc  bool

	archives []ArchiveEntry
	tasks    []TaskEntry
	seq      int

	// taskMu protects the concurrency accounting and FIFO queue. All App
	// fields are normally touched on the UI thread; the mutex also keeps the
	// accounting safe when task results arrive from worker goroutines.
	taskMu       sync.Mutex
	runningTasks int
	pendingTasks []pendingTask

	progressUnsub func()

	// Create-archive drawer.
	addOpen  bool
	sources  []string
	target   string
	format   string
	level    string
	password string

	// Extract drawer.
	extractOpen     bool
	extractPath     string
	extractPassword string
	outputDir       string

	// Update state.
	updateOpen     bool
	updateInfo     *contract.UpdateInfo
	updateChecking bool
	updateState    string // "", checking, available, downloading, ready, uptodate, error
	updateProgress float64
	updateErr      string
	updateCancel   context.CancelFunc
	autoCheck      bool
	lastCheck      time.Time
	statePath      string

	// Toast state: View shows a toast whenever seq changes.
	toastSeq   int
	toastShown int
	toastType  feedback.ToastType
	toastTitle string
	toastDesc  string
}

// New creates the app on top of the given service boundary.
func New(svc contract.Service) *App {
	if svc == nil {
		panic("desktop: nil service")
	}
	a := &App{
		svc:       svc,
		filter:    "all",
		sortAsc:   false,
		format:    "zip",
		level:     "",
		autoCheck: svc.AutoCheckEnabled(),
	}
	if bmp, err := ui.DecodeBitmap(logoPNG); err == nil {
		a.logo = bmp
	}
	a.loadState()
	return a
}

// Attach binds the native window and starts background services.
func (a *App) Attach(win *mygo.Window) {
	a.win = win
	a.progressUnsub = a.svc.SubscribeArchiveProgress(a.handleArchiveProgress)
	a.startTray()
	win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting || !a.closeTray {
			return
		}
		e.PreventDefault()
		win.Hide()
	})
	a.handleLaunchRequest()
	win.Invalidate()
}

func (a *App) startTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "zip233",
		Icon:           trayPNG,
		IconIsTemplate: true,
		ToolTip:        "zip233 归档管理器",
		Menu:           a.trayMenu(),
	})
	if err != nil {
		a.closeTray = false
		return
	}
	a.tray = tray
	a.closeTray = true
}

func (a *App) trayMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开 zip233", Click: func(*mygo.MenuItem, *mygo.Window) {
			if a.win != nil {
				a.win.Show()
				a.win.Focus()
			}
		}},
		mygo.Separator(),
		{Label: "检查更新", Click: func(*mygo.MenuItem, *mygo.Window) { a.checkUpdates(true) }},
		{Role: mygo.RoleQuit, Click: func(*mygo.MenuItem, *mygo.Window) { a.Quit() }},
	})
}

// Quit exits through MyGo so OnBeforeQuit can persist state.
func (a *App) Quit() {
	a.quitting = true
	mygo.App.Quit()
}

// handleLaunchRequest consumes the shell "open" and advanced-compress actions.
func (a *App) handleLaunchRequest() {
	req := a.svc.LaunchRequest()
	if req.Path == nil || strings.TrimSpace(*req.Path) == "" {
		return
	}
	path := *req.Path
	switch req.Action {
	case "open", "extract":
		a.openArchivePath(path)
	case "compress-advanced", "compress-advanced-here":
		a.onMain(func() {
			if !containsString(a.sources, path) {
				a.sources = append(a.sources, path)
			}
			a.target = a.suggestedName()
			a.addOpen = true
			a.page = pageWorkbench
		})
	}
}

// Window returns the bound window, if any.
func (a *App) Window() *mygo.Window { return a.win }

func (a *App) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// onMain runs fn on the UI thread and refreshes the view afterwards.
func (a *App) onMain(fn func()) {
	mygo.RunOnMain(func() {
		fn()
		a.invalidate()
	})
}

// handleArchiveProgress hands the update to the UI thread.
func (a *App) handleArchiveProgress(p contract.ArchiveProgress) {
	a.onMain(func() { a.applyArchiveProgress(p) })
}

// applyArchiveProgress attributes one update to a task. JobID is authoritative
// when present: several tasks of the same kind can run at once, so matching by
// phase alone would send progress to the wrong row. Legacy events with an
// empty JobID fall back to the newest running task of the same kind.
func (a *App) applyArchiveProgress(p contract.ArchiveProgress) {
	var kind taskKind
	switch p.Phase {
	case "create":
		kind = taskCreate
	case "extract":
		kind = taskExtract
	case "test":
		kind = taskTest
	default:
		return
	}
	if p.JobID != "" {
		for i := range a.tasks {
			if a.tasks[i].ID != p.JobID {
				continue
			}
			// A late update for a finished task must not overwrite its result,
			// nor fall back to another task of the same kind.
			if a.tasks[i].Status == taskRunning {
				a.setTaskProgress(i, p)
			}
			return
		}
	}
	for i := range a.tasks {
		if a.tasks[i].Status == taskRunning && a.tasks[i].Kind == kind {
			a.setTaskProgress(i, p)
			return
		}
	}
}

func (a *App) setTaskProgress(i int, p contract.ArchiveProgress) {
	a.tasks[i].Done = p.Done
	a.tasks[i].Total = p.Total
	if p.Message != "" {
		a.tasks[i].Message = p.Message
	}
	a.invalidate()
}

func (a *App) showToast(kind feedback.ToastType, title, description string) {
	a.toastType = kind
	a.toastTitle = title
	a.toastDesc = description
	a.toastSeq++
	a.invalidate()
}

func (a *App) nextID(prefix string) string {
	a.seq++
	return fmt.Sprintf("%s-%d", prefix, a.seq)
}

// selectedArchive returns the currently selected archive, if still present.
func (a *App) selectedArchive() (ArchiveEntry, bool) {
	for _, entry := range a.archives {
		if entry.ID == a.selected {
			return entry, true
		}
	}
	return ArchiveEntry{}, false
}

// latestTask returns the most recent task for an archive path. New tasks are
// always prepended, so the first match is the newest one; the old code scanned
// the whole slice and compared Start times for every row on every repaint.
func (a *App) latestTask(path string) (TaskEntry, bool) {
	for _, t := range a.tasks {
		if t.Path == path {
			return t, true
		}
	}
	return TaskEntry{}, false
}

func (a *App) visibleArchives() []ArchiveEntry {
	query := strings.ToLower(strings.TrimSpace(a.search))
	out := make([]ArchiveEntry, 0, len(a.archives))
	for _, entry := range a.archives {
		latest, hasTask := a.latestTask(entry.Path)
		switch a.filter {
		case "archives":
			if entry.Items <= 0 {
				continue
			}
		case "creating":
			if !hasTask || latest.Kind != taskCreate ||
				(latest.Status != taskRunning && latest.Status != taskQueued) {
				continue
			}
		case "extracting":
			if !hasTask || latest.Kind != taskExtract ||
				(latest.Status != taskRunning && latest.Status != taskQueued) {
				continue
			}
		case "failed":
			if !hasTask || latest.Status != taskFailed {
				continue
			}
		}
		if query != "" {
			hay := strings.ToLower(entry.Name + " " + entry.Path + " " + entry.Format)
			if !strings.Contains(hay, query) {
				continue
			}
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a.sortAsc {
			return out[i].Added.Before(out[j].Added)
		}
		return out[i].Added.After(out[j].Added)
	})
	return out
}

// ---------- 归档操作 ----------

// openArchive picks an archive, lists it, and adds it to the workbench.
func (a *App) openArchive() {
	go func() {
		p, err := a.svc.PickArchive()
		if err != nil || p == nil || strings.TrimSpace(*p) == "" {
			return
		}
		a.openArchivePath(*p)
	}()
}

// openArchivePath lists an archive passed by the file association and selects it.
func (a *App) openArchivePath(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	go func() {
		items, listErr := a.svc.ListArchive(path, "")
		a.onMain(func() {
			entry := ArchiveEntry{
				ID:     firstArchiveID(a.archives, path),
				Path:   path,
				Name:   filepath.Base(path),
				Format: formatFromPath(path),
				Size:   fileSize(path),
				Items:  len(items),
				Added:  time.Now(),
				Note:   "",
			}
			if listErr != nil {
				entry.Note = "无法列出内容，可能需要密码：" + listErr.Error()
			}
			if existingID := firstArchiveID(a.archives, path); existingID != "" {
				entry.ID = existingID
				entry.Password = archivePassword(a.archives, existingID)
				entry.Added = archiveAdded(a.archives, existingID)
			} else {
				entry.ID = a.nextID("arch")
			}
			upsertArchive(&a.archives, entry)
			a.selected = entry.ID
			a.page = pageWorkbench
			a.filter = "all"
			a.persist()
			if listErr != nil {
				a.showToast(feedback.ToastWarning, "已添加归档", entry.Note)
			} else {
				a.showToast(feedback.ToastSuccess, "已打开归档", fmt.Sprintf("共 %d 个项目", len(items)))
			}
		})
	}()
}

func firstArchiveID(archives []ArchiveEntry, path string) string {
	for _, entry := range archives {
		if entry.Path == path {
			return entry.ID
		}
	}
	return ""
}

func archivePassword(archives []ArchiveEntry, id string) string {
	for _, entry := range archives {
		if entry.ID == id {
			return entry.Password
		}
	}
	return ""
}

func archiveAdded(archives []ArchiveEntry, id string) time.Time {
	for _, entry := range archives {
		if entry.ID == id {
			return entry.Added
		}
	}
	return time.Now()
}

func upsertArchive(archives *[]ArchiveEntry, entry ArchiveEntry) {
	for i := range *archives {
		if (*archives)[i].ID == entry.ID || (*archives)[i].Path == entry.Path {
			(*archives)[i] = entry
			return
		}
	}
	*archives = append(*archives, entry)
}

// addSourceFiles picks files to compress.
func (a *App) addSourceFiles() {
	go func() {
		paths, err := a.svc.PickSources()
		if err != nil || len(paths) == 0 {
			return
		}
		a.onMain(func() {
			for _, p := range paths {
				if !containsString(a.sources, p) {
					a.sources = append(a.sources, p)
				}
			}
		})
	}()
}

// addSourceFolder picks a folder to compress.
func (a *App) addSourceFolder() {
	go func() {
		p, err := a.svc.PickSourceFolder()
		if err != nil || p == nil || strings.TrimSpace(*p) == "" {
			return
		}
		a.onMain(func() {
			if !containsString(a.sources, *p) {
				a.sources = append(a.sources, *p)
			}
		})
	}()
}

// chooseTarget opens the archive save dialog.
func (a *App) chooseTarget() {
	extension := strings.TrimPrefix(strings.TrimSpace(a.format), ".")
	go func() {
		p, err := a.svc.SaveArchive(a.suggestedName(), extension)
		if err != nil || p == nil || strings.TrimSpace(*p) == "" {
			return
		}
		a.onMain(func() { a.target = *p })
	}()
}

func (a *App) suggestedName() string {
	if len(a.sources) == 1 {
		name := filepath.Base(strings.TrimRight(a.sources[0], `\/`))
		if name != "" && name != "." {
			return name
		}
	}
	return "archive"
}

// startCreate queues a create-archive operation in the background.
func (a *App) startCreate() {
	paths := append([]string(nil), a.sources...)
	target := strings.TrimSpace(a.target)
	format := strings.TrimSpace(a.format)
	if format == "" {
		format = "zip"
	}
	level := a.level
	password := a.password
	if len(paths) == 0 {
		a.showToast(feedback.ToastWarning, "请先添加源文件", "至少选择一个文件或文件夹。")
		return
	}
	if target == "" {
		a.showToast(feedback.ToastWarning, "请选择目标文件", "请指定压缩包的保存位置。")
		return
	}
	task := TaskEntry{
		ID:      a.nextID("task"),
		Kind:    taskCreate,
		Name:    filepath.Base(target),
		Path:    target,
		Detail:  fmt.Sprintf("压缩 %d 项 → %s", len(paths), format),
		Status:  taskQueued,
		Message: "排队中…",
		Done:    0,
		Total:   int64(len(paths)),
		Started: time.Now(),
	}
	a.tasks = append([]TaskEntry{task}, a.tasks...)
	a.addOpen = false
	a.filter = "creating"
	a.page = pageWorkbench
	a.invalidate()

	a.submitTask(task.ID, "正在创建压缩包…", func() {
		go func() {
			result, err := a.svc.CreateArchiveWithJob(task.ID, paths, target, format, level, password)
			a.onMain(func() {
				a.finishTask(task.ID, result, err, "压缩包已创建", "创建压缩包失败")
				if err == nil && result.OK {
					upsertArchive(&a.archives, ArchiveEntry{
						ID:       a.nextID("arch"),
						Path:     target,
						Name:     filepath.Base(target),
						Format:   format,
						Size:     fileSize(target),
						Password: password,
						Added:    time.Now(),
					})
					a.selected = firstArchiveID(a.archives, target)
				}
			})
		}()
	})
}

// openExtractDrawer prepares the extract form for the selected archive.
func (a *App) openExtractDrawer() {
	entry, ok := a.selectedArchive()
	if !ok {
		a.showToast(feedback.ToastInfo, "请先选择归档", "在列表中选择一个归档后再解压。")
		return
	}
	a.extractPath = entry.Path
	a.extractPassword = entry.Password
	if a.outputDir == "" {
		if dir, err := mygo.App.Path(mygo.PathDownloads); err == nil && dir != "" {
			a.outputDir = dir
		} else {
			a.outputDir = filepath.Dir(entry.Path)
		}
	}
	a.extractOpen = true
	a.invalidate()
}

// chooseOutputDir picks the extraction destination.
func (a *App) chooseOutputDir() {
	go func() {
		p, err := a.svc.PickOutputFolder()
		if err != nil || p == nil || strings.TrimSpace(*p) == "" {
			return
		}
		a.onMain(func() { a.outputDir = *p; a.persist() })
	}()
}

// startExtract queues an extract operation in the background.
func (a *App) startExtract() {
	source := strings.TrimSpace(a.extractPath)
	output := strings.TrimSpace(a.outputDir)
	if source == "" {
		a.showToast(feedback.ToastWarning, "请先选择归档", "")
		return
	}
	if output == "" {
		a.showToast(feedback.ToastWarning, "请选择解压目标", "")
		return
	}
	password := a.extractPassword
	name := filepath.Base(source)
	task := TaskEntry{
		ID:      a.nextID("task"),
		Kind:    taskExtract,
		Name:    name,
		Path:    source,
		Detail:  "解压到 " + output,
		Status:  taskQueued,
		Message: "排队中…",
		Total:   1,
		Started: time.Now(),
	}
	a.tasks = append([]TaskEntry{task}, a.tasks...)
	a.extractOpen = false
	a.filter = "extracting"
	a.page = pageWorkbench
	a.invalidate()

	a.submitTask(task.ID, "正在解压…", func() {
		go func() {
			result, err := a.svc.ExtractArchiveWithJob(task.ID, source, output, password)
			a.onMain(func() {
				a.finishTask(task.ID, result, err, "解压完成", "解压失败")
				for i := range a.archives {
					if a.archives[i].Path == source {
						a.archives[i].Password = password
					}
				}
			})
		}()
	})
}

// testArchive runs a test operation in the background.
func (a *App) testArchive() {
	entry, ok := a.selectedArchive()
	if !ok {
		a.showToast(feedback.ToastInfo, "请先选择归档", "")
		return
	}
	path := entry.Path
	password := entry.Password
	task := TaskEntry{
		ID:      a.nextID("task"),
		Kind:    taskTest,
		Name:    entry.Name,
		Path:    path,
		Detail:  "完整性测试",
		Status:  taskQueued,
		Message: "排队中…",
		Total:   1,
		Started: time.Now(),
	}
	a.tasks = append([]TaskEntry{task}, a.tasks...)
	a.invalidate()

	a.submitTask(task.ID, "正在测试归档…", func() {
		go func() {
			result, err := a.svc.TestArchiveWithJob(task.ID, path, password)
			a.onMain(func() {
				a.finishTask(task.ID, result, err, "归档测试通过", "归档测试失败")
			})
		}()
	})
}

// finishTask applies a synchronous archive result to its task entry.
func (a *App) finishTask(id string, result contract.ToolResult, err error, okText, failText string) {
	for i := range a.tasks {
		if a.tasks[i].ID != id {
			continue
		}
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		switch {
		case err != nil:
			a.tasks[i].Status = taskFailed
			a.tasks[i].Message = err.Error()
			a.showToast(feedback.ToastError, failText, err.Error())
		case !result.OK:
			a.tasks[i].Status = taskFailed
			a.tasks[i].Message = detail
			a.showToast(feedback.ToastError, failText, detail)
		default:
			a.tasks[i].Status = taskDone
			a.tasks[i].Message = okText
			if a.tasks[i].Total > 0 {
				a.tasks[i].Done = a.tasks[i].Total
			}
			a.showToast(feedback.ToastSuccess, okText, detail)
		}
		if a.filter == "creating" || a.filter == "extracting" {
			a.filter = "all"
		}
		a.persist()
		a.taskFinished()
		return
	}
}

// removeArchive drops an archive and its tasks from the workbench.
func (a *App) removeArchive(id string) {
	removedPath := archivePath(a.archives, id)
	for i, entry := range a.archives {
		if entry.ID == id {
			a.archives = append(a.archives[:i], a.archives[i+1:]...)
			break
		}
	}
	tasks := a.tasks[:0]
	for _, t := range a.tasks {
		if t.Path != "" && t.Path == removedPath {
			continue
		}
		tasks = append(tasks, t)
	}
	a.tasks = tasks
	a.discardPending()
	if a.selected == id {
		a.selected = ""
	}
	a.persist()
	a.invalidate()
}

func archivePath(archives []ArchiveEntry, id string) string {
	for _, entry := range archives {
		if entry.ID == id {
			return entry.Path
		}
	}
	return ""
}

func (a *App) clearFinishedTasks() {
	tasks := a.tasks[:0]
	for _, t := range a.tasks {
		if t.Status != taskDone && t.Status != taskFailed {
			tasks = append(tasks, t)
		}
	}
	a.tasks = tasks
	a.discardPending()
	a.persist()
	a.invalidate()
}

// ---------- 系统 / 更新 ----------

func (a *App) reveal(path string) {
	if path == "" {
		return
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		_ = mygo.Shell.OpenPath(path)
		return
	}
	mygo.Shell.ShowItemInFolder(path)
}

func (a *App) copyPath(path string) {
	if path == "" {
		return
	}
	mygo.Clipboard.WriteText(path)
	a.showToast(feedback.ToastSuccess, "路径已复制", path)
}

// refreshShellState reads the Explorer integration state on the UI thread.
func (a *App) refreshShellState() contract.ShellStatus { return a.svc.ShellStatus() }

func (a *App) registerShell() {
	status, err := a.svc.RegisterShell()
	a.onMain(func() {
		if err != nil {
			a.showToast(feedback.ToastError, "注册右键菜单失败", err.Error())
		} else if status.Healthy {
			a.showToast(feedback.ToastSuccess, "右键菜单已注册", "")
		} else {
			a.showToast(feedback.ToastWarning, "右键菜单状态异常", "请检查注册表权限。")
		}
	})
}

func (a *App) unregisterShell() {
	_, err := a.svc.UnregisterShell()
	a.onMain(func() {
		if err != nil {
			a.showToast(feedback.ToastError, "移除右键菜单失败", err.Error())
		} else {
			a.showToast(feedback.ToastSuccess, "右键菜单已移除", "")
		}
	})
}

// checkUpdates performs a release check in the background.
func (a *App) checkUpdates(manual bool) {
	if a.updateChecking {
		return
	}
	a.updateChecking = true
	a.updateState = "checking"
	a.updateErr = ""
	a.invalidate()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		info, err := a.svc.CheckUpdates(ctx)
		a.onMain(func() {
			a.updateChecking = false
			a.lastCheck = time.Now()
			switch {
			case err != nil:
				a.updateState = "error"
				a.updateErr = err.Error()
				if manual {
					a.showToast(feedback.ToastError, "检查更新失败", err.Error())
				}
			case info.Available && info.CanDownload:
				a.updateState = "available"
				a.updateInfo = &info
				if manual {
					a.showToast(feedback.ToastSuccess, "发现新版本", latestVersion(&info))
				}
			default:
				a.updateState = "uptodate"
				a.updateInfo = &info
				if manual {
					a.showToast(feedback.ToastSuccess, "已是最新版本", "")
				}
			}
		})
	}()
}

// downloadUpdate downloads the installer in the background and polls status.
func (a *App) downloadUpdate() {
	if a.updateState == "downloading" || a.updateState == "ready" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.updateCancel = cancel
	a.updateState = "downloading"
	a.updateProgress = 0
	a.updateErr = ""
	a.invalidate()
	go func() {
		defer close(done)
		_, err := a.svc.StartDownload(ctx)
		cancelled := ctx.Err() != nil
		state := a.svc.DownloadStatus()
		a.onMain(func() {
			a.updateCancel = nil
			if cancelled {
				a.updateState = ""
				a.updateProgress = 0
				return
			}
			if err != nil {
				a.updateState = "error"
				a.updateErr = err.Error()
				a.showToast(feedback.ToastError, "下载更新失败", err.Error())
				return
			}
			if state.FilePath != nil && *state.FilePath != "" {
				a.updateState = "ready"
				a.updateProgress = 100
				a.showToast(feedback.ToastSuccess, "更新包已就绪", "")
			} else if state.Status == "error" {
				a.updateState = "error"
				a.updateErr = state.Message
				a.showToast(feedback.ToastError, "下载更新失败", state.Message)
			} else {
				a.updateState = "error"
				a.updateErr = state.Message
			}
		})
	}()
	go a.pollDownload(ctx, done)
}

func (a *App) pollDownload(ctx context.Context, done <-chan struct{}) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			state := a.svc.DownloadStatus()
			pct := float64(0)
			if state.TotalBytes != nil && *state.TotalBytes > 0 {
				pct = float64(state.DownloadedBytes) * 100 / float64(*state.TotalBytes)
			}
			a.onMain(func() { a.updateProgress = pct })
		}
	}
}

func (a *App) cancelDownload() {
	if a.updateCancel != nil {
		a.updateCancel()
		a.updateCancel = nil
	}
}

func (a *App) installUpdate() {
	state := a.svc.DownloadStatus()
	if state.FilePath == nil || *state.FilePath == "" {
		a.downloadUpdate()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := a.svc.InstallUpdate(ctx); err != nil {
			a.onMain(func() {
				a.updateErr = err.Error()
				a.showToast(feedback.ToastError, "安装更新失败", err.Error())
			})
			return
		}
		a.onMain(func() { mygo.App.Quit() })
	}()
}

func (a *App) installLocalUpdate() {
	go func() {
		p, err := a.svc.PickUpdatePackage()
		if err != nil || p == nil || strings.TrimSpace(*p) == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := a.svc.InstallLocalUpdate(ctx, *p); err != nil {
			a.onMain(func() { a.showToast(feedback.ToastError, "安装本地更新失败", err.Error()) })
		} else {
			a.onMain(func() { mygo.App.Quit() })
		}
	}()
}

func (a *App) setAutoCheck(enabled bool) {
	a.autoCheck = enabled
	a.svc.SetAutoCheck(enabled)
	if enabled && time.Since(a.lastCheck) > time.Hour {
		a.checkUpdates(false)
	}
}

func latestVersion(info *contract.UpdateInfo) string {
	if info == nil || info.LatestVersion == nil {
		return ""
	}
	return *info.LatestVersion
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func formatFromPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	for _, format := range []string{"tar.gz", "tar.bz2", "tar.xz", "tar.zst"} {
		if strings.HasSuffix(base, "."+format) {
			return format
		}
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	if ext == "" {
		return "unknown"
	}
	return ext
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "—"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(n)
	index := 0
	for value >= 1024 && index < len(units)-1 {
		value /= 1024
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", value, units[index])
}

func versionText() string { return "v" + version.Version }
