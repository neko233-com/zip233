package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/neko233-com/zip233/internal/contract"
)

// Install launches the already-verified update package. It returns as soon as
// the installer process has started.
func Install(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stateMu.Lock()
	st := dlState
	stateMu.Unlock()

	if st.Status != "done" || st.FilePath == nil || strings.TrimSpace(*st.FilePath) == "" {
		return errors.New("没有已校验通过的更新包可安装，请先下载更新。")
	}
	if _, err := os.Stat(*st.FilePath); err != nil {
		return fmt.Errorf("更新包已不存在或无法访问：%w", err)
	}
	return installPackage(ctx, *st.FilePath)
}

// InstallLocal launches a user-selected local package after basic validation.
// The local package is NOT checked against GitHub's digest; callers should
// surface Status().Message to warn the user.
func InstallLocal(ctx context.Context, location string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	abs, err := filepath.Abs(strings.TrimSpace(location))
	if err != nil {
		return fmt.Errorf("本地包路径无效：%w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("本地包不存在或无法访问：%w", err)
	}
	if info.IsDir() {
		return errors.New("本地包不能是目录。")
	}

	switch ext := strings.ToLower(path.Ext(abs)); ext {
	case ".msi", ".exe", ".dmg":
		// supported
	default:
		return fmt.Errorf("不支持的本地安装包类型：%s（仅支持 .msi / .exe / .dmg）", path.Ext(abs))
	}
	if info.Size() < 512<<10 {
		return errors.New("本地包体积异常，可能不是完整的安装包。")
	}
	if info.Size() > 8<<30 {
		return errors.New("本地包体积异常，超过 8 GB。")
	}

	if err := installPackage(ctx, abs); err != nil {
		return err
	}
	setDownloadState(contract.UpdateDownloadState{
		Status: "local-install", FilePath: ptr(abs),
		Message: "本地包未校验官方摘要，已启动安装程序。",
	})
	return nil
}

func installPackage(ctx context.Context, location string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var cmd *exec.Cmd
	switch strings.ToLower(path.Ext(location)) {
	case ".msi":
		if runtime.GOOS != "windows" {
			return errors.New("当前系统无法安装 .msi 安装包。")
		}
		args := []string{"/i", location}
		if os.Getenv("ZIP233_UPDATE_QUIET") == "1" {
			args = append(args, "/qn", "/norestart")
		}
		cmd = exec.Command("msiexec.exe", args...)
	case ".exe":
		if runtime.GOOS != "windows" {
			return errors.New("当前系统无法运行 Windows .exe 安装包。")
		}
		cmd = exec.Command(location)
	case ".dmg":
		if runtime.GOOS != "darwin" {
			return errors.New("当前系统无法打开 macOS .dmg 安装包。")
		}
		cmd = exec.Command("open", location)
	default:
		return fmt.Errorf("不支持的安装包类型：%s", path.Ext(location))
	}

	// Start instead of Run/Wait: the installer outlives this function and the
	// caller's context must not terminate it.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动安装程序失败：%w", err)
	}
	return nil
}
