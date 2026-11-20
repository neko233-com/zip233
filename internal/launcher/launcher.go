// Package launcher 以浏览器“应用窗口”模式打开本地 UI，不依赖 Node/Electron。
package launcher

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const windowSize = "--window-size=1180,790"

// OpenApp 以独立应用窗口打开给定 URL。
// Windows/macOS 优先使用 Edge/Chrome 的 --app 模式，找不到时回退到系统默认浏览器。
func OpenApp(url string) error {
	switch runtime.GOOS {
	case "windows":
		return openWindows(url)
	case "darwin":
		return openMac(url)
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func openWindows(url string) error {
	// 显式路径优先，随后尝试 PATH 查找。
	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	for _, name := range []string{"msedge", "chrome"} {
		if p, err := exec.LookPath(name + ".exe"); err == nil {
			candidates = append(candidates, p)
		}
	}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		cmd := exec.Command(path, "--app="+url, windowSize)
		if err := startDetached(cmd); err == nil {
			return nil
		}
	}
	// 回退：系统默认浏览器（非 app 模式）。
	if err := startDetached(exec.Command("rundll32", "url.dll,FileProtocolHandler", url)); err == nil {
		return nil
	}
	return fmt.Errorf("无法打开浏览器窗口，请手动访问: %s", url)
}

func openMac(url string) error {
	candidates := []string{
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, path := range candidates {
		cmd := exec.Command(path, "--app="+url, windowSize)
		if err := startDetached(cmd); err == nil {
			return nil
		}
	}
	// 回退：系统默认浏览器。
	if err := startDetached(exec.Command("open", url)); err == nil {
		return nil
	}
	return fmt.Errorf("无法打开浏览器窗口，请手动访问: %s", url)
}

// startDetached 启动进程并释放句柄，使其独立于本进程运行。
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	// 子进程由系统接管，不阻塞父进程退出。
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

// IsAppScheme 仅供测试：确认传入的是本地 http URL。
func IsAppScheme(url string) bool {
	return strings.HasPrefix(url, "http://127.0.0.1:") || strings.HasPrefix(url, "http://localhost:")
}
