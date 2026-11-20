//go:build windows

// Package shell manages zip233's Windows Explorer context-menu integration.
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neko233-com/zip233/internal/contract"
	"golang.org/x/sys/windows/registry"
)

const (
	settingsKey = `Software\neko233\zip233`
	enabledName = "ShellIntegrationEnabled"
	menuIcon    = "zip233-menu.ico"
)

// archiveExtensions are the formats zip233 registers a "使用 zip233 打开" verb for.
var archiveExtensions = []string{
	"7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz",
	"Z", "taz", "ace", "arj", "arc", "pak", "zoo", "ha", "jar", "j", "uc2",
	"sq", "sq2", "qqq", "sqz", "hyp", "lzh", "lha", "ice", "pi9",
}

// legacyArchiveExtensions are no longer registered but must be cleaned up.
var legacyArchiveExtensions = []string{
	"zst", "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio",
	"chm", "vhd", "vhdx", "vmdk", "vdi", "qcow2", "udf", "xar", "apfs",
	"squashfs", "cramfs", "fat", "ntfs", "gpt", "mbr", "uefi", "ihex",
	"nsis", "msi", "hfs",
}

type fileVerb struct {
	suffix  string
	muiVerb string
	command string
}

var fileVerbs = []fileVerb{
	{suffix: "open", muiVerb: "使用 zip233 打开", command: ` open "%1"`},
	{suffix: "extract-here", muiVerb: "解压到这里", command: ` extract-here "%1"`},
	{suffix: "extract-to", muiVerb: "解压到文件夹…", command: ` extract-to "%1"`},
	{suffix: "test", muiVerb: "测试完整性", command: ` test-archive "%1"`},
}

var otherKeys = []string{
	`Software\Classes\Directory\shell\zip233.compress-zip`,
	`Software\Classes\Directory\shell\zip233.compress-advanced`,
	`Software\Classes\Directory\Background\shell\zip233.compress-zip-here`,
	`Software\Classes\Directory\Background\shell\zip233.compress-advanced-here`,
	`Software\Classes\Directory\shell\zip233.compress`,
	`Software\Classes\Directory\Background\shell\zip233.compress-here`,
}

type shellEntry struct {
	path    string
	muiVerb string
	command string
}

func fileAssocKey(ext string) string {
	return fileVerbKey(ext, "open")
}

func fileVerbKey(ext, verb string) string {
	return fmt.Sprintf(`Software\Classes\SystemFileAssociations\.%s\shell\zip233.%s`, ext, verb)
}

func currentExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法获取当前程序路径：%w", err)
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// iconPath prefers the dedicated menu icon next to the executable and falls
// back to the executable itself when the icon file is not installed.
func iconPath(exe string) string {
	candidate := filepath.Join(filepath.Dir(exe), menuIcon)
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate
	}
	return exe
}

func shellEntries(exe string) []shellEntry {
	q := `"` + exe + `"`
	entries := []shellEntry{
		{path: `Software\Classes\Directory\shell\zip233.compress-zip`, muiVerb: "直接压缩为 ZIP", command: q + ` compress-zip "%1"`},
		{path: `Software\Classes\Directory\shell\zip233.compress-advanced`, muiVerb: "zip233 高级压缩…", command: q + ` compress-advanced "%1"`},
		{path: `Software\Classes\Directory\Background\shell\zip233.compress-zip-here`, muiVerb: "直接压缩为 ZIP", command: q + ` compress-zip-here "%V"`},
		{path: `Software\Classes\Directory\Background\shell\zip233.compress-advanced-here`, muiVerb: "zip233 高级压缩…", command: q + ` compress-advanced-here "%V"`},
	}
	for _, ext := range archiveExtensions {
		for _, verb := range fileVerbs {
			entries = append(entries, shellEntry{
				path:    fileVerbKey(ext, verb.suffix),
				muiVerb: verb.muiVerb,
				command: q + verb.command,
			})
		}
	}
	return entries
}

// Status reports whether integration is enabled and all keys point at this exe.
func Status() contract.ShellStatus {
	enabled := isEnabled()
	healthy := false
	if enabled {
		if exe, err := currentExe(); err == nil {
			healthy = allEntriesHealthy(exe, iconPath(exe))
		}
	}
	return contract.ShellStatus{Enabled: enabled, Healthy: healthy}
}

// Register writes (or refreshes) every zip233-owned Explorer entry.
func Register() (contract.ShellStatus, error) {
	exe, err := currentExe()
	if err != nil {
		return Status(), err
	}
	icon := iconPath(exe)

	for _, entry := range shellEntries(exe) {
		if err := writeEntry(entry, icon); err != nil {
			return Status(), err
		}
	}
	cleanupLegacy()
	if err := setEnabled(true); err != nil {
		return Status(), err
	}
	return Status(), nil
}

// Unregister removes only zip233-owned keys and records Enabled=0.
func Unregister() (contract.ShellStatus, error) {
	for _, ext := range archiveExtensions {
		for _, verb := range fileVerbs {
			deleteTree(fileVerbKey(ext, verb.suffix))
		}
	}
	cleanupLegacy()
	for _, key := range otherKeys {
		deleteTree(key)
	}
	if err := setEnabled(false); err != nil {
		return Status(), err
	}
	return Status(), nil
}

// RepairIfEnabled rewrites all entries when the user had enabled integration.
func RepairIfEnabled() {
	if !isEnabled() {
		return
	}
	_, _ = Register()
}

func isEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, settingsKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	value, valueType, err := k.GetIntegerValue(enabledName)
	if err != nil || valueType != registry.DWORD {
		return false
	}
	return value == 1
}

func setEnabled(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, settingsKey, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("写入 zip233 设置失败：%w", err)
	}
	defer k.Close()

	var value uint32
	if on {
		value = 1
	}
	if err := k.SetDWordValue(enabledName, value); err != nil {
		return fmt.Errorf("写入右键菜单启用状态失败：%w", err)
	}
	return nil
}

func writeEntry(entry shellEntry, icon string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, entry.path, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("写入右键菜单项 %s 失败：%w", entry.path, err)
	}
	if err := k.SetStringValue("MUIVerb", entry.muiVerb); err != nil {
		k.Close()
		return fmt.Errorf("写入右键菜单名称失败：%w", err)
	}
	if err := k.SetStringValue("Icon", icon); err != nil {
		k.Close()
		return fmt.Errorf("写入右键菜单图标失败：%w", err)
	}
	k.Close()

	commandKey, _, err := registry.CreateKey(registry.CURRENT_USER, entry.path+`\command`, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("写入右键菜单命令失败：%w", err)
	}
	defer commandKey.Close()
	if err := commandKey.SetStringValue("", entry.command); err != nil {
		return fmt.Errorf("写入右键菜单命令失败：%w", err)
	}
	return nil
}

func allEntriesHealthy(exe, icon string) bool {
	for _, entry := range shellEntries(exe) {
		k, err := registry.OpenKey(registry.CURRENT_USER, entry.path, registry.QUERY_VALUE)
		if err != nil {
			return false
		}
		gotIcon, iconType, iconErr := k.GetStringValue("Icon")
		k.Close()
		if iconErr != nil || iconType != registry.SZ || !iconMatches(gotIcon, exe, icon) {
			return false
		}

		commandKey, err := registry.OpenKey(registry.CURRENT_USER, entry.path+`\command`, registry.QUERY_VALUE)
		if err != nil {
			return false
		}
		gotCommand, commandType, commandErr := commandKey.GetStringValue("")
		commandKey.Close()
		if commandErr != nil || commandType != registry.SZ ||
			!strings.EqualFold(strings.TrimSpace(gotCommand), strings.TrimSpace(entry.command)) {
			return false
		}
	}
	return true
}

func iconMatches(got, exe, icon string) bool {
	return pathEqual(got, icon) || pathEqual(got, exe)
}

func pathEqual(a, b string) bool {
	a = strings.Trim(strings.TrimSpace(a), `"`)
	b = strings.Trim(strings.TrimSpace(b), `"`)
	return strings.EqualFold(a, b)
}

func cleanupLegacy() {
	for _, ext := range legacyArchiveExtensions {
		for _, verb := range fileVerbs {
			deleteTree(fileVerbKey(ext, verb.suffix))
		}
	}
	for _, key := range otherKeys[len(otherKeys)-2:] {
		deleteTree(key)
	}
}

func deleteTree(keyPath string) {
	_ = registry.DeleteKey(registry.CURRENT_USER, keyPath+`\command`)
	_ = registry.DeleteKey(registry.CURRENT_USER, keyPath)
}
