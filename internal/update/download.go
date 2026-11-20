package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/neko233-com/zip233/internal/contract"
)

var mirrors = []string{
	"https://ghproxy.net/",
	"https://gh-proxy.com/",
	"https://ghfast.top/",
}

var downloadClient = &http.Client{}

type downloadResult struct {
	path string
	err  error
	url  string
}

// StartDownload races the official release URL against several mirrors. The
// first source to pass both SHA-256 and size validation wins.
func StartDownload(ctx context.Context) (contract.UpdateDownloadState, error) {
	if downloading.Load() {
		st := Status()
		st.Message = "已有更新下载任务正在进行。"
		return st, errors.New("已有更新下载任务正在进行")
	}
	downloading.Store(true)
	defer func() {
		stateMu.Lock()
		fn := cancelFunc
		cancelFunc = nil
		stateMu.Unlock()
		if fn != nil {
			fn()
		}
		downloading.Store(false)
	}()

	if err := ctx.Err(); err != nil {
		return errorState("下载已取消。", err)
	}

	stateMu.Lock()
	rel := cachedRelease
	info := cachedInfo
	stateMu.Unlock()

	if rel == nil || !info.CanDownload {
		fresh, err := Check(ctx)
		if err != nil {
			return errorState("检查更新失败，无法开始下载："+err.Error(), err)
		}
		if !fresh.CanDownload {
			return errorState(fresh.Message, errors.New(fresh.Message))
		}
		stateMu.Lock()
		rel = cachedRelease
		stateMu.Unlock()
	}
	if rel == nil {
		return errorState("尚未获取到更新信息，请先检查更新。", errors.New("尚未获取到更新信息"))
	}

	asset, reason := chooseAsset(runtime.GOOS, rel.TagName, rel.Assets)
	if asset == nil {
		return errorState(reason, errors.New(reason))
	}
	wantHash, ok := digestSHA256(asset.Digest)
	if !ok {
		return errorState("安装包缺少 GitHub 官方 SHA-256 摘要，出于安全考虑无法下载。",
			errors.New("安装包缺少官方 SHA-256 摘要"))
	}
	if strings.TrimSpace(asset.BrowserDownloadURL) == "" {
		return errorState("发布信息未提供安装包下载地址，无法下载。", errors.New("下载地址缺失"))
	}

	v := normalizeTag(rel.TagName)
	finalDir := filepath.Join(os.TempDir(), "zip233", "updates", v)
	finalPath := filepath.Join(finalDir, filepath.Base(asset.Name))
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return errorState("创建下载目录失败："+err.Error(), err)
	}

	// Reuse an already verified download.
	if verifyFile(finalPath, wantHash, asset.Size) == nil {
		st := doneState(v, asset, finalPath, asset.Size)
		setDownloadState(st)
		return st, nil
	}

	downloadAbort.Store(false)
	dlProgress.Store(0)
	workCtx, cancel := context.WithCancel(ctx)
	stateMu.Lock()
	cancelFunc = cancel
	stateMu.Unlock()

	setDownloadState(contract.UpdateDownloadState{
		Status: "downloading", Version: ptr(v), AssetName: ptr(asset.Name),
		DownloadedBytes: 0, TotalBytes: ptr(asset.Size), Message: "正在下载更新包…",
	})

	urls := downloadURLs(asset)
	results := make(chan downloadResult, len(urls))
	for _, rawURL := range urls {
		go func(u string) {
			p, err := downloadOne(workCtx, u, finalDir, asset, wantHash, &dlProgress)
			results <- downloadResult{path: p, err: err, url: u}
		}(rawURL)
	}

	var winner *downloadResult
	var errs []string
	remaining := len(urls)
	for remaining > 0 && winner == nil {
		r := <-results
		remaining--
		if downloadAbort.Load() || ctx.Err() != nil {
			break
		}
		if r.err == nil {
			winner = &r
			cancel() // stop the slower sources
		} else {
			errs = append(errs, sourceLabel(r.url)+"："+r.err.Error())
		}
	}
	if remaining > 0 {
		// Losers may still be streaming; reap their temp files in the background.
		go func(n int) {
			for ; n > 0; n-- {
				rr := <-results
				if rr.path != "" {
					_ = os.Remove(rr.path)
				}
			}
		}(remaining)
	}

	aborted := downloadAbort.Load() || ctx.Err() != nil
	if aborted {
		if winner != nil {
			_ = os.Remove(winner.path)
		}
		st := contract.UpdateDownloadState{
			Status: "cancelled", Version: ptr(v), AssetName: ptr(asset.Name),
			DownloadedBytes: dlProgress.Load(), TotalBytes: ptr(asset.Size),
			Message: "下载已取消。",
		}
		setDownloadState(st)
		return st, nil
	}
	if winner == nil {
		return errorState("下载失败："+strings.Join(errs, "；"), errors.New("下载失败"))
	}

	_ = os.Remove(finalPath) // Windows cannot rename over an existing file.
	if err := os.Rename(winner.path, finalPath); err != nil {
		_ = os.Remove(winner.path)
		return errorState("保存更新包失败："+err.Error(), err)
	}
	if err := verifyFile(finalPath, wantHash, asset.Size); err != nil {
		return errorState("更新包校验失败："+err.Error(), err)
	}

	st := doneState(v, asset, finalPath, asset.Size)
	setDownloadState(st)
	return st, nil
}

func doneState(v string, asset *releaseAsset, finalPath string, size uint64) contract.UpdateDownloadState {
	return contract.UpdateDownloadState{
		Status: "done", Version: ptr(v), AssetName: ptr(asset.Name),
		DownloadedBytes: size, TotalBytes: ptr(size),
		Message: "更新包已下载并通过官方 SHA-256 摘要校验。", FilePath: ptr(finalPath),
	}
}

func downloadURLs(asset *releaseAsset) []string {
	official := strings.TrimSpace(asset.BrowserDownloadURL)
	urls := []string{official}
	for _, m := range mirrors {
		urls = append(urls, strings.TrimRight(m, "/")+"/"+strings.TrimLeft(official, "/"))
	}
	return urls
}

func sourceLabel(raw string) string {
	for _, m := range mirrors {
		if strings.HasPrefix(raw, m) {
			return "镜像 " + strings.TrimRight(m, "/")
		}
	}
	return "官方源"
}

func downloadOne(ctx context.Context, rawURL, dir string, asset *releaseAsset, wantHash []byte, progress *atomic.Uint64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("构建下载请求失败：%w", err)
	}
	req.Header.Set("User-Agent", "zip233-updater")

	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP 状态码 %d", resp.StatusCode)
	}

	limit := asset.Size*2 + (1 << 20)
	if asset.Size > 0 && resp.ContentLength > 0 && uint64(resp.ContentLength) > limit {
		return "", fmt.Errorf("响应大小异常")
	}

	tmp, err := os.CreateTemp(dir, ".zip233-download-*")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败：%w", err)
	}
	tmpPath := tmp.Name()
	fail := func(format string, args ...any) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf(format, args...)
	}

	h := sha256.New()
	buf := make([]byte, 128*1024)
	var written uint64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, werr := tmp.Write(chunk); werr != nil {
				return fail("写入临时文件失败：%w", werr)
			}
			_, _ = h.Write(chunk)
			written += uint64(n)
			if written > limit {
				return fail("下载大小超出预期")
			}
			for {
				cur := progress.Load()
				if written <= cur || progress.CompareAndSwap(cur, written) {
					break
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fail("读取下载内容失败：%w", readErr)
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("关闭临时文件失败：%w", err)
	}

	if asset.Size > 0 && written != asset.Size {
		return fail("文件大小不匹配（期望 %d 字节，实际 %d 字节）", asset.Size, written)
	}
	if !bytes.Equal(h.Sum(nil), wantHash) {
		return fail("SHA-256 摘要与官方发布信息不一致")
	}
	return tmpPath, nil
}

func verifyFile(path string, wantHash []byte, size uint64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if size > 0 {
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if uint64(st.Size()) != size {
			return fmt.Errorf("文件大小不匹配（期望 %d 字节，实际 %d 字节）", size, st.Size())
		}
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), wantHash) {
		return errors.New("SHA-256 摘要与官方发布信息不一致")
	}
	return nil
}
