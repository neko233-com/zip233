// Package contract defines the JSON DTOs and service boundary shared by the
// Go GUI server, CLI helpers, platform adapters and packaging tools.
package contract

import "context"

// ArchiveItem is one entry in an archive listing.
type ArchiveItem struct {
	Name        string  `json:"name"`
	Size        *int64  `json:"size"`
	PackedSize  *int64  `json:"packed_size"`
	IsDirectory bool    `json:"is_directory"`
	Modified    *string `json:"modified"`
}

// ArchiveProgress reports one step of a long-running archive operation.
// JobID is empty for legacy progress events that did not carry a task id; UI
// consumers should fall back to matching by Phase in that case.
type ArchiveProgress struct {
	JobID   string `json:"job_id,omitempty"`
	Phase   string `json:"Phase"`
	Done    int64  `json:"Done"`
	Total   int64  `json:"Total"`
	Message string `json:"Message"`
}

// ToolResult is the uniform result payload for archive operations.
type ToolResult struct {
	OK     bool   `json:"ok"`
	Code   *int   `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// LaunchRequest captures the shell command that launched the GUI.
type LaunchRequest struct {
	Action string  `json:"action"`
	Path   *string `json:"path"`
}

// ShellStatus reports Windows Explorer integration state.
type ShellStatus struct {
	Enabled bool `json:"enabled"`
	Healthy bool `json:"healthy"`
}

// UpdateInfo describes the newest stable GitHub release.
type UpdateInfo struct {
	CurrentVersion string  `json:"current_version"`
	LatestVersion  *string `json:"latest_version"`
	Available      bool    `json:"available"`
	ReleaseURL     *string `json:"release_url"`
	ReleaseNotes   *string `json:"release_notes"`
	AssetName      *string `json:"asset_name"`
	AssetSize      *uint64 `json:"asset_size"`
	CanDownload    bool    `json:"can_download"`
	Message        string  `json:"message"`
}

// UpdateDownloadState is the progress/result of a verified update download.
type UpdateDownloadState struct {
	Status          string  `json:"status"`
	Version         *string `json:"version"`
	AssetName       *string `json:"asset_name"`
	DownloadedBytes uint64  `json:"downloaded_bytes"`
	TotalBytes      *uint64 `json:"total_bytes"`
	Message         string  `json:"message"`
	FilePath        *string `json:"file_path"`
}

// Service is the boundary consumed by the embedded HTTP API.
type Service interface {
	ListArchive(path, password string) ([]ArchiveItem, error)
	TestArchive(path, password string) (ToolResult, error)
	ExtractArchive(path, outputDir, password string) (ToolResult, error)
	CreateArchive(paths []string, target, format, level, password string) (ToolResult, error)

	// *WithJob variants tag progress with jobID so several archive operations
	// can run concurrently without their progress being attributed to the
	// wrong task. Implementations that cannot support job IDs should still
	// expose these methods (they may simply delegate to the plain variants).
	TestArchiveWithJob(jobID, path, password string) (ToolResult, error)
	ExtractArchiveWithJob(jobID, path, outputDir, password string) (ToolResult, error)
	CreateArchiveWithJob(jobID string, paths []string, target, format, level, password string) (ToolResult, error)

	PickArchive() (*string, error)
	PickSources() ([]string, error)
	PickSourceFolder() (*string, error)
	PickOutputFolder() (*string, error)
	SaveArchive(defaultName, extension string) (*string, error)
	PickUpdatePackage() (*string, error)

	ShellStatus() ShellStatus
	RegisterShell() (ShellStatus, error)
	UnregisterShell() (ShellStatus, error)
	LaunchRequest() LaunchRequest

	CheckUpdates(ctx context.Context) (UpdateInfo, error)
	StartDownload(ctx context.Context) (UpdateDownloadState, error)
	DownloadStatus() UpdateDownloadState
	CancelDownload() error
	InstallUpdate(ctx context.Context) error
	InstallLocalUpdate(ctx context.Context, path string) error

	SetAutoCheck(enabled bool)
	AutoCheckEnabled() bool

	SubscribeArchiveProgress(fn func(ArchiveProgress)) func()
}
