// Package update checks GitHub Releases, downloads verified installers and
// launches the platform installer. All user-facing strings are Chinese.
package update

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/neko233-com/zip233/internal/contract"
	"github.com/neko233-com/zip233/internal/version"
)

var (
	stateMu       sync.Mutex
	cachedRelease *githubRelease
	cachedInfo    contract.UpdateInfo

	dlState       = contract.UpdateDownloadState{Status: "idle", Message: "暂无下载任务。"}
	dlProgress    atomic.Uint64
	cancelFunc    context.CancelFunc
	downloading   atomic.Bool
	downloadAbort atomic.Bool
)

// Check queries the latest stable GitHub release and describes the result.
func Check(ctx context.Context) (contract.UpdateInfo, error) {
	rel, err := fetchLatestRelease(ctx)
	if err != nil {
		return contract.UpdateInfo{
			CurrentVersion: version.Version,
			Message:        "检查更新失败：" + err.Error(),
		}, err
	}

	info := buildUpdateInfo(rel, runtime.GOOS)
	stateMu.Lock()
	cachedRelease = rel
	cachedInfo = info
	stateMu.Unlock()
	return info, nil
}

// Status returns a snapshot of the current download state.
func Status() contract.UpdateDownloadState {
	stateMu.Lock()
	st := dlState
	stateMu.Unlock()
	if st.Status == "downloading" {
		st.DownloadedBytes = dlProgress.Load()
	}
	return st
}

// Cancel aborts an in-flight download. It is safe to call when idle.
func Cancel() error {
	if !downloading.Load() {
		return nil
	}
	downloadAbort.Store(true)

	stateMu.Lock()
	fn := cancelFunc
	dlState = contract.UpdateDownloadState{Status: "cancelled", Message: "下载已取消。"}
	stateMu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

func setDownloadState(st contract.UpdateDownloadState) {
	stateMu.Lock()
	dlState = st
	stateMu.Unlock()
}

func errorState(message string, err error) (contract.UpdateDownloadState, error) {
	st := contract.UpdateDownloadState{Status: "error", Message: message}
	setDownloadState(st)
	return st, err
}

// MaybeAutoCheck performs a background check/download when auto-check is on
// and the last check is older than six hours. It never installs anything.
func MaybeAutoCheck(ctx context.Context) {
	cfg := loadConfig()
	if !cfg.AutoCheck || timeSince(cfg.LastCheckAt) < autoCheckInterval {
		return
	}

	cfg.LastCheckAt = configNow()
	saveConfig(cfg)

	info, err := Check(ctx)
	if err != nil || !info.CanDownload {
		return
	}
	_, _ = StartDownload(ctx)
}
