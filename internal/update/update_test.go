package update

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.1", "0.2.0", 1},
		{"0.2.0", "0.2.1", -1},
		{"v0.2.0", "0.2.0", 0},
		{"1.0.0", "0.9.9", 1},
		{"0.3.0", "0.2.99", 1},
		{"0.2.0", "0.2.0-rc.1", 1},
		{"0.2.0-rc.2", "0.2.0-rc.1", 1},
		{"0.2.0-beta", "0.2.0-rc.1", -1},
		{"0.2.0+build", "0.2.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); sign(got) != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func sign(n int) int {
	if n > 0 {
		return 1
	}
	if n < 0 {
		return -1
	}
	return 0
}

func TestChooseAsset(t *testing.T) {
	assets := []releaseAsset{
		{Name: "zip233-0.2.1-x64-setup.exe"},
		{Name: "zip233-0.2.1-x64.msi"},
		{Name: "zip233-0.2.1.dmg"},
		{Name: "source.tar.gz"},
	}

	win, _ := chooseAsset("windows", "v0.2.1", assets)
	if win == nil || win.Name != "zip233-0.2.1-x64.msi" {
		t.Fatalf("windows: got %v, want msi", win)
	}

	mac, _ := chooseAsset("darwin", "v0.2.1", assets)
	if mac == nil || mac.Name != "zip233-0.2.1.dmg" {
		t.Fatalf("darwin: got %v, want dmg", mac)
	}

	noMsi := []releaseAsset{{Name: "zip233-0.2.1-x64-setup.exe"}, {Name: "random.exe"}}
	win2, _ := chooseAsset("windows", "v0.2.1", noMsi)
	if win2 == nil || win2.Name != "zip233-0.2.1-x64-setup.exe" {
		t.Fatalf("windows fallback: got %v, want setup.exe", win2)
	}

	anyExe := []releaseAsset{{Name: "whatever-x64.exe"}}
	win3, _ := chooseAsset("windows", "v0.2.1", anyExe)
	if win3 == nil || win3.Name != "whatever-x64.exe" {
		t.Fatalf("windows any exe: got %v", win3)
	}

	if a, reason := chooseAsset("windows", "v0.2.1", []releaseAsset{{Name: "source.tar.gz"}}); a != nil || reason == "" {
		t.Fatalf("windows no asset: got %v, reason %q", a, reason)
	}
	if a, reason := chooseAsset("linux", "v0.2.1", assets); a != nil || reason == "" {
		t.Fatalf("linux: got %v, reason %q", a, reason)
	}
}

func TestDigestSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte("zip233"))
	good := "sha256:" + hex.EncodeToString(sum[:])
	if got, ok := digestSHA256(good); !ok || string(got) != string(sum[:]) {
		t.Fatalf("valid digest rejected: %q", good)
	}
	for _, bad := range []string{"", "md5:abc", "sha256:deadbeef", "sha256:" + strings.Repeat("z", 64)} {
		if _, ok := digestSHA256(bad); ok {
			t.Errorf("invalid digest accepted: %q", bad)
		}
	}
}

func TestDownloadURLs(t *testing.T) {
	a := &releaseAsset{BrowserDownloadURL: "https://github.com/neko233-com/zip233/releases/download/v0.2.1/zip233-0.2.1-x64.msi"}
	urls := downloadURLs(a)
	if len(urls) != 4 {
		t.Fatalf("got %d urls, want 4", len(urls))
	}
	for i, prefix := range []string{"https://github.com/", "https://ghproxy.net/", "https://gh-proxy.com/", "https://ghfast.top/"} {
		if !strings.HasPrefix(urls[i], prefix) {
			t.Errorf("url[%d] = %q, want prefix %q", i, urls[i], prefix)
		}
	}
}
