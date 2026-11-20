package update

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/neko233-com/zip233/internal/contract"
	"github.com/neko233-com/zip233/internal/version"
)

const latestReleaseURL = "https://api.github.com/repos/neko233-com/zip233/releases/latest"

var apiClient = &http.Client{Timeout: 20 * time.Second}

// releaseAsset is the subset of the GitHub Release asset object we need.
type releaseAsset struct {
	Name               string `json:"name"`
	Size               uint64 `json:"size"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Body    string         `json:"body"`
	Assets  []releaseAsset `json:"assets"`
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构建更新检查请求失败：%w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "zip233-updater")

	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 发布信息失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 发布接口返回状态码 %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 GitHub 发布信息失败：%w", err)
	}

	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("解析 GitHub 发布信息失败：%w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, errors.New("GitHub 发布信息缺少版本号")
	}
	applyChecksumSidecars(ctx, &rel)
	return &rel, nil
}

// applyChecksumSidecars fills missing GitHub asset digests from DownloadX-style
// `<asset>.sha256` release assets. This keeps auto-update working for releases
// where the API omits the digest field but publishes a checksum sidecar.
func applyChecksumSidecars(ctx context.Context, rel *githubRelease) {
	for i := range rel.Assets {
		asset := &rel.Assets[i]
		if _, ok := digestSHA256(asset.Digest); ok || strings.HasSuffix(strings.ToLower(asset.Name), ".sha256") {
			continue
		}
		sidecar := findAsset(rel.Assets, asset.Name+".sha256")
		if sidecar == nil || strings.TrimSpace(sidecar.BrowserDownloadURL) == "" {
			continue
		}
		sum, ok := fetchChecksumFile(ctx, sidecar.BrowserDownloadURL)
		if ok {
			asset.Digest = "sha256:" + sum
		}
	}
}

func fetchChecksumFile(ctx context.Context, url string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "zip233-updater")
	resp, err := apiClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return "", false
	}
	sum := strings.ToLower(strings.TrimSpace(fields[0]))
	if len(sum) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", false
	}
	return sum, true
}

func findAsset(assets []releaseAsset, name string) *releaseAsset {
	for i := range assets {
		if strings.EqualFold(assets[i].Name, name) {
			return &assets[i]
		}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

// normalizeTag turns "v0.2.1" into "0.2.1".
func normalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

// digestSHA256 validates GitHub's "sha256:<64 hex>" digest field.
func digestSHA256(digest string) ([]byte, bool) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(digest)), "sha256:") {
		return nil, false
	}
	raw := strings.TrimSpace(digest[len("sha256:"):])
	if len(raw) != 64 {
		return nil, false
	}
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) != 32 {
		return nil, false
	}
	return b, true
}

// versionParts parses a semver-ish string into numeric core and prerelease.
func versionParts(s string) ([3]int, string) {
	var nums [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err == nil && n >= 0 {
			nums[i] = n
		}
	}
	return nums, pre
}

// compareVersions returns >0 if a is newer than b, 0 if equal, <0 if older.
func compareVersions(a, b string) int {
	an, ap := versionParts(a)
	bn, bp := versionParts(b)
	for i := 0; i < 3; i++ {
		if an[i] > bn[i] {
			return 1
		}
		if an[i] < bn[i] {
			return -1
		}
	}
	if ap == bp {
		return 0
	}
	if ap == "" {
		return 1 // release > prerelease
	}
	if bp == "" {
		return -1
	}
	// Compare prerelease identifiers dot by dot.
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		c := comparePreIdentifier(as[i], bs[i])
		if c != 0 {
			return c
		}
	}
	if len(as) > len(bs) {
		return 1
	}
	if len(as) < len(bs) {
		return -1
	}
	return 0
}

func comparePreIdentifier(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		if an > bn {
			return 1
		}
		if an < bn {
			return -1
		}
		return 0
	}
	if aerr == nil {
		return -1 // numeric identifiers have lower precedence
	}
	if berr == nil {
		return 1
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

// chooseAsset picks the best installer asset for the running OS.
// reason is a Chinese explanation when no asset matched.
func chooseAsset(goos, tag string, assets []releaseAsset) (*releaseAsset, string) {
	v := normalizeTag(tag)
	find := func(name string) *releaseAsset {
		for i := range assets {
			if strings.EqualFold(assets[i].Name, name) {
				return &assets[i]
			}
		}
		return nil
	}
	findExt := func(ext string) *releaseAsset {
		for i := range assets {
			if strings.EqualFold(path.Ext(assets[i].Name), ext) {
				return &assets[i]
			}
		}
		return nil
	}

	switch goos {
	case "windows":
		for _, name := range []string{
			fmt.Sprintf("zip233-%s-x64.msi", v),
			"zip233-windows-amd64.msi",
			fmt.Sprintf("zip233-%s-x64-setup.exe", v),
			"zip233-windows-amd64-setup.exe",
		} {
			if a := find(name); a != nil {
				return a, ""
			}
		}
		if a := findExt(".exe"); a != nil {
			return a, ""
		}
		return nil, "发现新版本，但未找到适用于 Windows 的安装包（.msi 或 .exe）。"
	case "darwin":
		if a := find(fmt.Sprintf("zip233-%s.dmg", v)); a != nil {
			return a, ""
		}
		if a := findExt(".dmg"); a != nil {
			return a, ""
		}
		return nil, "发现新版本，但未找到适用于 macOS 的安装包（.dmg）。"
	default:
		return nil, fmt.Sprintf("当前系统（%s）暂不支持自动更新。", goos)
	}
}

// buildUpdateInfo turns a fetched release into the public DTO and caches it.
func buildUpdateInfo(rel *githubRelease, goos string) contract.UpdateInfo {
	latest := normalizeTag(rel.TagName)
	info := contract.UpdateInfo{
		CurrentVersion: version.Version,
		LatestVersion:  ptr(latest),
		ReleaseURL:     ptr(rel.HTMLURL),
		ReleaseNotes:   ptr(rel.Body),
		Available:      compareVersions(latest, version.Version) > 0,
	}

	asset, reason := chooseAsset(goos, rel.TagName, rel.Assets)
	if asset != nil {
		info.AssetName = ptr(asset.Name)
		info.AssetSize = ptr(asset.Size)
	}

	switch {
	case !info.Available:
		info.Message = "当前已是最新版本。"
	case asset == nil:
		info.Message = reason
	case !validDigest(asset.Digest):
		info.Message = "发现新版本，但安装包缺少 GitHub 官方 SHA-256 摘要，出于安全考虑无法下载。"
	default:
		info.Message = fmt.Sprintf("发现新版本 %s，可下载更新。", latest)
		info.CanDownload = true
	}
	return info
}

func validDigest(digest string) bool {
	_, ok := digestSHA256(digest)
	return ok
}
