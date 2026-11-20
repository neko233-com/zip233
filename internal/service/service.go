// Package service adapts the Go subsystems to the local HTTP API boundary.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neko233-com/zip233/internal/archive"
	"github.com/neko233-com/zip233/internal/contract"
	"github.com/neko233-com/zip233/internal/picker"
	"github.com/neko233-com/zip233/internal/shell"
	"github.com/neko233-com/zip233/internal/update"
)

// Service implements contract.Service using the in-process Go engines.
type Service struct {
	progressMu   sync.RWMutex
	progressSubs map[int]func(contract.ArchiveProgress)
	progressNext int
}

// New creates the service and starts the persisted automatic update check.
func New() *Service {
	s := &Service{progressSubs: make(map[int]func(contract.ArchiveProgress))}
	// Legacy global callback: plain Create/Test/Extract (HTTP API, shell
	// actions, CLI-style callers) still emit with an empty JobID. Job-aware
	// desktop calls use a per-job callback instead, so the two never compete
	// for one global slot.
	archive.SetProgress(func(p archive.Progress) { s.publish(convertProgress(p)) })
	go update.MaybeAutoCheck(context.Background())
	go shell.RepairIfEnabled()
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			update.MaybeAutoCheck(context.Background())
		}
	}()
	return s
}

var _ contract.Service = (*Service)(nil)

func convertProgress(p archive.Progress) contract.ArchiveProgress {
	return contract.ArchiveProgress{
		JobID: p.JobID, Phase: p.Phase, Done: p.Done, Total: p.Total, Message: p.Message,
	}
}

// publish fans one update out to every subscriber. Subscribers are copied
// under the lock so slow or re-entrant callbacks cannot block registration.
func (s *Service) publish(p contract.ArchiveProgress) {
	s.progressMu.RLock()
	callbacks := make([]func(contract.ArchiveProgress), 0, len(s.progressSubs))
	for _, fn := range s.progressSubs {
		callbacks = append(callbacks, fn)
	}
	s.progressMu.RUnlock()
	for _, fn := range callbacks {
		fn(p)
	}
}

// jobContext builds an archive context whose progress callback belongs to a
// single job. Each concurrent operation gets its own closure, so progress is
// always attributed to the job that produced it.
func (s *Service) jobContext(jobID string) context.Context {
	return archive.WithProgress(context.Background(), jobID, func(p archive.Progress) {
		s.publish(convertProgress(p))
	})
}

// SubscribeArchiveProgress registers a UI callback and returns an unsubscribe function.
func (s *Service) SubscribeArchiveProgress(fn func(contract.ArchiveProgress)) func() {
	s.progressMu.Lock()
	s.progressNext++
	id := s.progressNext
	s.progressSubs[id] = fn
	s.progressMu.Unlock()
	return func() {
		s.progressMu.Lock()
		delete(s.progressSubs, id)
		s.progressMu.Unlock()
	}
}

func (s *Service) ListArchive(path, password string) ([]contract.ArchiveItem, error) {
	return archive.List(path, password)
}

func (s *Service) TestArchive(path, password string) (contract.ToolResult, error) {
	return archive.Test(path, password)
}

func (s *Service) ExtractArchive(path, outputDir, password string) (contract.ToolResult, error) {
	return archive.Extract(path, outputDir, password)
}

func (s *Service) CreateArchive(paths []string, target, format, level, password string) (contract.ToolResult, error) {
	return archive.Create(paths, target, format, level, password)
}

// TestArchiveWithJob runs a test with progress tagged by jobID.
func (s *Service) TestArchiveWithJob(jobID, path, password string) (contract.ToolResult, error) {
	return archive.TestWithContext(s.jobContext(jobID), path, password)
}

// ExtractArchiveWithJob runs an extraction with progress tagged by jobID.
func (s *Service) ExtractArchiveWithJob(jobID, path, outputDir, password string) (contract.ToolResult, error) {
	return archive.ExtractWithContext(s.jobContext(jobID), path, outputDir, password)
}

// CreateArchiveWithJob runs a creation with progress tagged by jobID.
func (s *Service) CreateArchiveWithJob(jobID string, paths []string, target, format, level, password string) (contract.ToolResult, error) {
	return archive.CreateWithContext(s.jobContext(jobID), paths, target, format, level, password)
}

func (s *Service) PickArchive() (*string, error) {
	return picker.PickFile("打开归档", archiveFilter())
}

func (s *Service) PickSources() ([]string, error) {
	return picker.PickFiles("选择要压缩的文件")
}

func (s *Service) PickSourceFolder() (*string, error) {
	return picker.PickFolder("选择要压缩的文件夹")
}

func (s *Service) PickOutputFolder() (*string, error) {
	return picker.PickFolder("选择解压目标文件夹")
}

func (s *Service) SaveArchive(defaultName, extension string) (*string, error) {
	extensions := []string{extension}
	if extension == "" {
		extensions = archive.SupportedCreateFormats()
	}
	return picker.SaveFile("保存归档文件", defaultName, extensions)
}

func (s *Service) PickUpdatePackage() (*string, error) {
	return picker.PickFile("选择本地更新包", []string{"msi", "exe", "dmg"})
}

func (s *Service) ShellStatus() contract.ShellStatus {
	return shell.Status()
}

func (s *Service) RegisterShell() (contract.ShellStatus, error) {
	return shell.Register()
}

func (s *Service) UnregisterShell() (contract.ShellStatus, error) {
	return shell.Unregister()
}

func (s *Service) LaunchRequest() contract.LaunchRequest {
	args := os.Args[1:]
	if len(args) == 0 {
		return contract.LaunchRequest{Action: ""}
	}
	request := contract.LaunchRequest{Action: args[0]}
	if len(args) > 1 {
		path := args[1]
		request.Path = &path
	}
	return request
}

func (s *Service) CheckUpdates(ctx context.Context) (contract.UpdateInfo, error) {
	return update.Check(ctx)
}

func (s *Service) StartDownload(ctx context.Context) (contract.UpdateDownloadState, error) {
	return update.StartDownload(ctx)
}

func (s *Service) DownloadStatus() contract.UpdateDownloadState {
	return update.Status()
}

func (s *Service) CancelDownload() error {
	return update.Cancel()
}

func (s *Service) InstallUpdate(ctx context.Context) error {
	return update.Install(ctx)
}

func (s *Service) InstallLocalUpdate(ctx context.Context, path string) error {
	return update.InstallLocal(ctx, path)
}

func (s *Service) SetAutoCheck(enabled bool) {
	update.SetAutoCheck(enabled)
}

func (s *Service) AutoCheckEnabled() bool {
	return update.AutoCheckEnabled()
}

// RunShellAction performs the silent Explorer actions without opening a window.
func (s *Service) RunShellAction(action, path string) error {
	err := s.runShellAction(action, path)
	if err != nil {
		reportShellError(err)
	}
	return err
}

func (s *Service) runShellAction(action, path string) error {
	switch action {
	case "compress-zip":
		directory, err := canonicalDirectory(path)
		if err != nil {
			return err
		}
		parent := filepath.Dir(directory)
		name := filepath.Base(directory)
		target, cleanup, err := reserveZip(parent, name)
		if err != nil {
			return err
		}
		if err := archive.DirectCompressZip([]string{directory}, target); err != nil {
			cleanup()
			return err
		}
		return nil
	case "compress-zip-here":
		directory, err := canonicalDirectory(path)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("无法读取当前文件夹：%w", err)
		}
		var sources []string
		for _, entry := range entries {
			sources = append(sources, filepath.Join(directory, entry.Name()))
		}
		if len(sources) == 0 {
			return errors.New("当前文件夹为空，没有可压缩的内容。")
		}
		name := filepath.Base(directory)
		target, cleanup, err := reserveZip(directory, name)
		if err != nil {
			return err
		}
		if err := archive.DirectCompressZip(sources, target); err != nil {
			cleanup()
			return err
		}
		return nil
	case "extract-here":
		archivePath, err := canonicalArchiveFile(path)
		if err != nil {
			return err
		}
		output := filepath.Dir(archivePath)
		result, err := archive.Extract(archivePath, output, "")
		if err != nil {
			return passwordHint(err)
		}
		if !result.OK {
			return passwordHint(errors.New(result.Stderr))
		}
		return nil
	case "extract-to":
		archivePath, err := canonicalArchiveFile(path)
		if err != nil {
			return err
		}
		output, err := picker.PickFolder("选择解压目标文件夹")
		if err != nil {
			return err
		}
		if output == nil {
			return nil
		}
		result, err := archive.Extract(archivePath, *output, "")
		if err != nil {
			return passwordHint(err)
		}
		if !result.OK {
			return passwordHint(errors.New(result.Stderr))
		}
		return nil
	case "test-archive":
		archivePath, err := canonicalArchiveFile(path)
		if err != nil {
			return err
		}
		result, err := archive.Test(archivePath, "")
		if err != nil {
			return passwordHint(err)
		}
		if !result.OK {
			return passwordHint(errors.New(result.Stderr))
		}
		message := strings.TrimSpace(result.Stdout)
		if message == "" {
			message = "完整性检查通过。"
		}
		reportShellSuccess(message)
		return nil
	default:
		return fmt.Errorf("未知的 zip233 操作：%s", action)
	}
}

func canonicalArchiveFile(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("无法解析归档路径：%w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("无法访问归档 %s：%w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s 是文件夹，不是归档。", path)
	}
	return absolute, nil
}

func passwordHint(err error) error {
	message := err.Error()
	if regexpPassword.MatchString(message) {
		return fmt.Errorf("%w\n该归档可能需要密码；请打开 zip233 输入密码后再操作。", err)
	}
	return err
}

var regexpPassword = regexp.MustCompile(`(?i)password|密码|encrypted|加密`)

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("无法解析文件夹路径：%w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("无法访问文件夹 %s：%w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s 不是文件夹。", path)
	}
	return absolute, nil
}

func reserveZip(directory, base string) (string, func(), error) {
	for index := 1; index <= 9999; index++ {
		name := base + ".zip"
		if index > 1 {
			name = fmt.Sprintf("%s (%d).zip", base, index)
		}
		candidate := filepath.Join(directory, name)
		file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", func() {}, fmt.Errorf("无法创建 ZIP 文件 %s：%w", candidate, err)
		}
		_ = file.Close()
		cleanup := func() { _ = os.Remove(candidate) }
		return candidate, cleanup, nil
	}
	return "", func() {}, errors.New("同名 ZIP 文件太多，无法生成新的归档名称。")
}

func archiveFilter() []string {
	all := append([]string{}, archive.ArchiveExtensions...)
	all = append(all, archive.LegacyArchiveExtensions...)
	sort.Strings(all)
	seen := make(map[string]bool, len(all))
	result := make([]string, 0, len(all))
	for _, extension := range all {
		key := strings.ToLower(extension)
		if !seen[key] {
			seen[key] = true
			result = append(result, extension)
		}
	}
	return result
}
