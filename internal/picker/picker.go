// Package picker 提供跨平台原生文件/文件夹选择对话框，不依赖 cgo，也不带任何 UI 框架。
//
// Windows 通过 PowerShell + System.Windows.Forms 弹原生对话框；
// macOS 通过 osascript；Linux 通过 zenity（缺失时返回中文错误）。
// 返回 nil 路径表示用户取消了对话框。
package picker

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// PickFile 打开单个文件选择对话框。filters 可以是扩展名（"zip"、".7z"、"*.rar"），
// 也可以是系统原生 filter 字符串（含 "|"）。返回 nil 表示用户取消。
func PickFile(title string, filters []string) (*string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := runPowerShell(winOpenDialogScript(title, winFilter(filters), false))
		if err != nil {
			return nil, err
		}
		return oneOrNil(out), nil
	case "darwin":
		script := fmt.Sprintf("set p to choose file with prompt %s%s\nreturn POSIX path of p",
			asQuote(title), macTypeClause(filters))
		out, err := runOSAScript(script)
		if err != nil {
			return nil, err
		}
		return oneOrNil(out), nil
	default:
		list, err := linuxPickFile(title, filters, false)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, nil
		}
		return &list[0], nil
	}
}

// PickFiles 打开文件多选对话框，返回所选文件路径列表；取消时返回 nil。
// 压缩场景需要选择任意类型文件，因此不限制扩展名。
func PickFiles(title string) ([]string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := runPowerShell(winOpenDialogScript(title, winFilter(nil), true))
		if err != nil {
			return nil, err
		}
		return lines(out), nil
	case "darwin":
		script := fmt.Sprintf(`set ps to choose file with prompt %s%s with multiple selections allowed
set out to ""
repeat with p in ps
	set out to out & (POSIX path of p) & linefeed
end repeat
return out`, asQuote(title), macTypeClause(nil))
		out, err := runOSAScript(script)
		if err != nil {
			return nil, err
		}
		return lines(out), nil
	default:
		return linuxPickFile(title, nil, true)
	}
}

// PickFolder 打开文件夹选择对话框。返回 nil 表示用户取消。
func PickFolder(title string) (*string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := runPowerShell(winFolderDialogScript(title))
		if err != nil {
			return nil, err
		}
		return oneOrNil(out), nil
	case "darwin":
		script := fmt.Sprintf("set p to choose folder with prompt %s\nreturn POSIX path of p", asQuote(title))
		out, err := runOSAScript(script)
		if err != nil {
			return nil, err
		}
		return oneOrNil(out), nil
	default:
		return linuxPickFolder(title)
	}
}

// SaveFile 打开保存文件对话框。extensions 为扩展名列表（如 "zip"、"*.7z"）。返回 nil 表示用户取消。
func SaveFile(title, defaultName string, extensions []string) (*string, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := runPowerShell(winSaveDialogScript(title, defaultName, winFilter(extensions)))
		if err != nil {
			return nil, err
		}
		return oneOrNil(out), nil
	case "darwin":
		script := fmt.Sprintf("set p to choose file name with prompt %s default name %s\nreturn POSIX path of p",
			asQuote(title), asQuote(defaultName))
		out, err := runOSAScript(script)
		if err != nil {
			return nil, err
		}
		return oneOrNil(out), nil
	default:
		return linuxSaveFile(title, defaultName, extensions)
	}
}

// ---------- 通用辅助 ----------

func oneOrNil(out string) *string {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	return &out
}

func lines(out string) []string {
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func cleanPS(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "\ufeff"))
}

func extNames(filters []string) []string {
	var exts []string
	for _, f := range filters {
		if strings.Contains(f, "|") || strings.TrimSpace(f) == "" {
			continue
		}
		e := strings.TrimSpace(f)
		e = strings.TrimPrefix(e, "*")
		e = strings.TrimPrefix(e, ".")
		if e != "" {
			exts = append(exts, e)
		}
	}
	return exts
}

// ---------- Windows: PowerShell + WinForms ----------

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func winFilter(filters []string) string {
	if len(filters) == 0 {
		return "所有文件 (*.*)|*.*"
	}
	var parts []string
	for _, f := range filters {
		if strings.Contains(f, "|") {
			parts = append(parts, f)
			continue
		}
		pattern := strings.TrimSpace(f)
		if !strings.HasPrefix(pattern, "*.") {
			if strings.HasPrefix(pattern, ".") {
				pattern = "*" + pattern
			} else {
				pattern = "*." + pattern
			}
		}
		parts = append(parts, pattern+" 文件 ("+pattern+")|"+pattern)
	}
	parts = append(parts, "所有文件 (*.*)|*.*")
	return strings.Join(parts, "|")
}

func winPreamble() string {
	return "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $OutputEncoding = [System.Text.Encoding]::UTF8; $ErrorActionPreference = 'Stop'; Add-Type -AssemblyName System.Windows.Forms; "
}

func winOpenDialogScript(title, filter string, multi bool) string {
	multiVal := "$false"
	if multi {
		multiVal = "$true"
	}
	return winPreamble() + fmt.Sprintf(`
$dlg = New-Object System.Windows.Forms.OpenFileDialog
$dlg.Title = %s
$dlg.Filter = %s
$dlg.Multiselect = %s
try {
	if ($dlg.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
		if (%s) { [string]::Join([string][char]10, $dlg.FileNames) } else { $dlg.FileName }
	}
} finally { $dlg.Dispose() }
`, psQuote(title), psQuote(filter), multiVal, multiVal)
}

func winFolderDialogScript(title string) string {
	return winPreamble() + fmt.Sprintf(`
$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
$dlg.Description = %s
$dlg.ShowNewFolderButton = $true
try {
	if ($dlg.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $dlg.SelectedPath }
} finally { $dlg.Dispose() }
`, psQuote(title))
}

func winSaveDialogScript(title, defaultName, filter string) string {
	return winPreamble() + fmt.Sprintf(`
$dlg = New-Object System.Windows.Forms.SaveFileDialog
$dlg.Title = %s
$dlg.Filter = %s
$dlg.FileName = %s
$dlg.OverwritePrompt = $true
try {
	if ($dlg.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $dlg.FileName }
} finally { $dlg.Dispose() }
`, psQuote(title), psQuote(filter), psQuote(defaultName))
}

func runPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := cleanPS(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("文件选择对话框打开失败: %s", msg)
	}
	return cleanPS(stdout.String()), nil
}

// ---------- macOS: osascript ----------

func asQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func macTypeClause(filters []string) string {
	exts := extNames(filters)
	if len(exts) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(exts))
	for _, e := range exts {
		quoted = append(quoted, asQuote(e))
	}
	return " of type {" + strings.Join(quoted, ", ") + "}"
}

func runOSAScript(script string) (string, error) {
	cmd := exec.Command("osascript", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		// 用户取消时 osascript 返回 -128，视为正常取消。
		if strings.Contains(msg, "-128") {
			return "", nil
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("文件选择对话框打开失败: %s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ---------- Linux: zenity ----------

func requireZenity() error {
	if _, err := exec.LookPath("zenity"); err != nil {
		return fmt.Errorf("未找到 zenity，请先安装：文件选择功能不可用")
	}
	return nil
}

func zenityFilterArgs(filters []string) []string {
	var args []string
	// zenity 的 --file-filter 形如 "名称|*.zip *.7z"，最后补充“所有文件”。
	for _, f := range filters {
		if strings.Contains(f, "|") {
			parts := strings.SplitN(f, "|", 2)
			args = append(args, "--file-filter="+parts[0]+"|"+parts[1])
			continue
		}
		ext := strings.TrimSpace(f)
		if !strings.HasPrefix(ext, "*.") {
			if strings.HasPrefix(ext, ".") {
				ext = "*" + ext
			} else {
				ext = "*." + ext
			}
		}
		args = append(args, "--file-filter="+ext+" 文件| "+ext)
	}
	args = append(args, "--file-filter=所有文件| *")
	return args
}

func runZenity(args ...string) (string, error) {
	if err := requireZenity(); err != nil {
		return "", err
	}
	cmd := exec.Command("zenity", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	// zenity 取消时退出码为 1 且无输出。
	if err != nil && out == "" {
		return "", nil
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("文件选择对话框打开失败: %s", msg)
	}
	return out, nil
}

func linuxPickFile(title string, filters []string, multi bool) ([]string, error) {
	args := []string{"--file-selection", "--title=" + title}
	if multi {
		args = append(args, "--multiple", "--separator=\n")
	}
	args = append(args, zenityFilterArgs(filters)...)
	out, err := runZenity(args...)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

func linuxPickFolder(title string) (*string, error) {
	out, err := runZenity("--file-selection", "--directory", "--title="+title)
	if err != nil {
		return nil, err
	}
	return oneOrNil(out), nil
}

func linuxSaveFile(title, defaultName string, extensions []string) (*string, error) {
	args := []string{"--file-selection", "--save", "--confirm-overwrite", "--title=" + title}
	if defaultName != "" {
		args = append(args, "--filename="+defaultName)
	}
	args = append(args, zenityFilterArgs(extensions)...)
	out, err := runZenity(args...)
	if err != nil {
		return nil, err
	}
	return oneOrNil(out), nil
}
