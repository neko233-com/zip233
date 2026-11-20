package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// redirectTransport sends every request to the httptest server while keeping
// the original path, so fetchLatestRelease can be exercised without touching
// the real GitHub API.
type redirectTransport struct{ base *url.URL }

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.base.Scheme
	clone.URL.Host = rt.base.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func withTestAPI(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	original := apiClient
	t.Cleanup(func() { apiClient = original })
	apiClient = &http.Client{Transport: redirectTransport{base: base}, Timeout: original.Timeout}
}

func testDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func releaseJSON(t *testing.T, rel githubRelease) string {
	t.Helper()
	data, err := json.Marshal(rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestChooseAssetPrefersVersionedNames(t *testing.T) {
	versionedMSI := releaseAsset{Name: "zip233-1.2.3-x64.msi"}
	stableMSI := releaseAsset{Name: "zip233-windows-amd64.msi"}
	versionedSetup := releaseAsset{Name: "zip233-1.2.3-x64-setup.exe"}
	stableSetup := releaseAsset{Name: "zip233-windows-amd64-setup.exe"}

	cases := []struct {
		name   string
		goos   string
		assets []releaseAsset
		want   string
	}{
		{"versioned-msi-over-stable-msi", "windows", []releaseAsset{stableMSI, versionedMSI}, versionedMSI.Name},
		// 格式优先级（MSI > EXE）高于跨格式的版本化优先级。
		{"stable-msi-over-versioned-setup", "windows", []releaseAsset{stableMSI, versionedSetup}, stableMSI.Name},
		{"versioned-setup-over-stable-setup", "windows", []releaseAsset{stableSetup, versionedSetup}, versionedSetup.Name},
		{"stable-msi-over-stable-setup", "windows", []releaseAsset{stableSetup, stableMSI}, stableMSI.Name},
		{"stable-msi-fallback", "windows", []releaseAsset{stableMSI}, stableMSI.Name},
		{"versioned-dmg", "darwin", []releaseAsset{
			{Name: "zip233-old.dmg"}, {Name: "zip233-1.2.3.dmg"},
		}, "zip233-1.2.3.dmg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := chooseAsset(tc.goos, "v1.2.3", tc.assets)
			if got == nil || got.Name != tc.want || reason != "" {
				t.Fatalf("chooseAsset = %v, %q; want %s", got, reason, tc.want)
			}
		})
	}
}

func TestSidecarBackfillsDigest(t *testing.T) {
	msiName := "zip233-99.0.0-x64.msi"
	sum := testDigest("msi-payload")
	withTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/neko233-com/zip233/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, releaseJSON(t, githubRelease{
				TagName: "v99.0.0",
				HTMLURL: "https://example.test/releases/v99.0.0",
				Body:    "notes",
				Assets: []releaseAsset{
					{Name: msiName, Size: 4096, BrowserDownloadURL: "https://example.test/download/msi"},
					{Name: msiName + ".sha256", BrowserDownloadURL: "https://example.test/checksums/msi.sha256"},
				},
			}))
		case "/checksums/msi.sha256":
			fmt.Fprintf(w, "%s  %s\n", sum, msiName)
		default:
			http.NotFound(w, r)
		}
	}))

	rel, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease 失败：%v", err)
	}
	asset := findAsset(rel.Assets, msiName)
	if asset == nil || asset.Digest != "sha256:"+sum {
		t.Fatalf("sidecar 未回填 digest：%+v", asset)
	}
	info := buildUpdateInfo(rel, "windows")
	if !info.Available || !info.CanDownload || info.AssetName == nil || *info.AssetName != msiName {
		t.Fatalf("buildUpdateInfo 结果错误：%+v", info)
	}
}

func TestGitHubDigestTakesPrecedenceOverSidecar(t *testing.T) {
	msiName := "zip233-99.0.0-x64.msi"
	githubSum := testDigest("github-digest")
	sidecarSum := testDigest("sidecar-digest")
	var sidecarFetched atomic.Bool
	withTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/neko233-com/zip233/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, releaseJSON(t, githubRelease{
				TagName: "v99.0.0",
				Assets: []releaseAsset{
					{Name: msiName, Digest: "sha256:" + githubSum, BrowserDownloadURL: "https://example.test/download/msi"},
					{Name: msiName + ".sha256", BrowserDownloadURL: "https://example.test/checksums/msi.sha256"},
				},
			}))
		case "/checksums/msi.sha256":
			sidecarFetched.Store(true)
			fmt.Fprintf(w, "%s  %s\n", sidecarSum, msiName)
		default:
			http.NotFound(w, r)
		}
	}))

	rel, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sidecarFetched.Load() {
		t.Error("GitHub digest 已存在时不应请求 sidecar")
	}
	if got := findAsset(rel.Assets, msiName); got == nil || got.Digest != "sha256:"+githubSum {
		t.Fatalf("GitHub digest 应优先：%+v", got)
	}
	if info := buildUpdateInfo(rel, "windows"); !info.CanDownload {
		t.Error("有效 GitHub digest 应允许下载")
	}
}

func TestInvalidSidecarDisablesDownload(t *testing.T) {
	msiName := "zip233-99.0.0-x64.msi"
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"not-hex", "not-a-valid-checksum\n", http.StatusOK},
		{"wrong-length", strings.Repeat("a", 63) + "\n", http.StatusOK},
		{"http-error", testDigest("payload"), http.StatusInternalServerError},
		{"empty", "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/neko233-com/zip233/releases/latest":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, releaseJSON(t, githubRelease{
						TagName: "v99.0.0",
						Assets: []releaseAsset{
							{Name: msiName, BrowserDownloadURL: "https://example.test/download/msi"},
							{Name: msiName + ".sha256", BrowserDownloadURL: "https://example.test/checksums/msi.sha256"},
						},
					}))
				case "/checksums/msi.sha256":
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				default:
					http.NotFound(w, r)
				}
			}))

			rel, err := fetchLatestRelease(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := findAsset(rel.Assets, msiName); got == nil || got.Digest != "" {
				t.Fatalf("无效 sidecar 不应写入 digest：%+v", got)
			}
			info := buildUpdateInfo(rel, "windows")
			if info.CanDownload || info.Available != true {
				t.Fatalf("无效 sidecar 时 CanDownload 应为 false：%+v", info)
			}
		})
	}
}

func TestNoSidecarNoDigestDisablesDownload(t *testing.T) {
	withTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, releaseJSON(t, githubRelease{
			TagName: "v99.0.0",
			Assets: []releaseAsset{
				{Name: "zip233-99.0.0-x64.msi", BrowserDownloadURL: "https://example.test/download/msi"},
			},
		}))
	}))
	rel, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info := buildUpdateInfo(rel, "windows"); info.CanDownload {
		t.Fatalf("缺少摘要时不应允许下载：%+v", info)
	}
}
