// Command zip233-pack builds the release binaries and native installers.
//
// It must run on the native host of the target platform (Windows or macOS):
//
//	go run ./cmd/zip233-pack
//
// Cross-building via -os is supported for the Go binaries, but the installer
// tools (makensis/WiX/hdiutil) are host-specific.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/neko233-com/zip233/internal/version"
)

type options struct {
	goos       string
	out        string
	version    string
	skipBuild  bool
	skipBundle bool
}

func main() {
	if err := execute(); err != nil {
		fmt.Fprintf(os.Stderr, "打包失败: %v\n", err)
		os.Exit(1)
	}
}

func execute() error {
	var opts options
	flag.StringVar(&opts.goos, "os", runtime.GOOS, "目标系统：windows / darwin / linux（默认当前主机）")
	flag.StringVar(&opts.out, "out", "", "release 输出目录（默认 target/release）")
	flag.StringVar(&opts.version, "version", "", "版本号（默认取 internal/version.Version）")
	flag.BoolVar(&opts.skipBuild, "skip-build", false, "跳过 go build，只生成安装包")
	flag.BoolVar(&opts.skipBundle, "skip-bundle", false, "只构建二进制，不生成安装包")
	flag.Parse()

	goos := strings.ToLower(opts.goos)
	switch goos {
	case "windows", "darwin", "linux":
	default:
		return fmt.Errorf("不支持的目标系统 %q（仅支持 windows/darwin/linux）", goos)
	}

	root := findModuleRoot()
	releaseDir := filepath.Join(root, "target", "release")
	if opts.out != "" {
		releaseDir = opts.out
	}
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	ver := version.Version
	if opts.version != "" {
		ver = strings.TrimPrefix(strings.TrimSpace(opts.version), "v")
	}
	fmt.Printf("zip233 打包: 版本 %s, 目标 %s\n", ver, goos)

	if !opts.skipBuild {
		if err := buildBinaries(root, releaseDir, goos); err != nil {
			return err
		}
	} else {
		fmt.Println("  跳过二进制构建 (-skip-build)")
	}
	if opts.skipBundle {
		fmt.Println("  跳过安装包生成 (-skip-bundle)")
		return nil
	}

	switch goos {
	case "windows":
		return bundleWindows(root, releaseDir, ver)
	case "darwin":
		return bundleMacOS(root, releaseDir, ver)
	case "linux":
		fmt.Println("提示: Linux 暂不提供桌面安装包（MSI/NSIS/DMG），已完成二进制构建。")
		return nil
	}
	return nil
}

func buildBinaries(root, releaseDir, goos string) error {
	suffix := ""
	if goos == "windows" {
		suffix = ".exe"
	}
	targets := []struct {
		pkg  string
		name string
	}{
		{"./cmd/zip233", "zip233" + suffix},
		{"./cmd/zip233-cli", "zip233-cli" + suffix},
	}

	env := []string{"CGO_ENABLED=0"}
	if goos != runtime.GOOS {
		env = append(env, "GOOS="+goos)
		if goos == "windows" {
			env = append(env, "GOARCH=amd64")
		}
	}

	for _, target := range targets {
		out := filepath.Join(releaseDir, target.name)
		fmt.Printf("构建 %s -> %s\n", target.pkg, out)
		if err := runInEnv(root, env, "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, target.pkg); err != nil {
			return fmt.Errorf("构建 %s 失败: %w", target.pkg, err)
		}
	}
	return nil
}

// joinErrors combines the optional NSIS/MSI failures so one missing tool does
// not prevent the other installer from being produced.
func joinErrors(errs ...error) error {
	var filtered []error
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	return errors.Join(filtered...)
}
