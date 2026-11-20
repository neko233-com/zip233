package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// bundleWindows generates a per-user NSIS setup.exe and a per-machine MSI.
// Generation of the sources always happens, even when a tool is missing, so
// users can inspect/fix the scripts locally or feed them to CI.
func bundleWindows(root, releaseDir, ver string) error {
	return joinErrors(
		buildNSIS(root, releaseDir, ver),
		buildMSI(root, releaseDir, ver),
	)
}

func buildNSIS(root, releaseDir, ver string) error {
	nsisDir := filepath.Join(releaseDir, "bundle", "nsis")
	if err := os.MkdirAll(nsisDir, 0o755); err != nil {
		return fmt.Errorf("创建 NSIS 目录失败: %w", err)
	}
	script, err := buildNSISScript(root, releaseDir, ver)
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(nsisDir, "installer.nsi")
	// A UTF-8 BOM makes NSIS read the Chinese menu labels correctly.
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(script)...)
	if err := os.WriteFile(scriptPath, content, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", scriptPath, err)
	}
	fmt.Printf("已生成 NSIS 脚本: %s\n", scriptPath)

	if _, err := lookTool("makensis", "请安装 NSIS 并将 makensis 加入 PATH（CI 会自动安装）"); err != nil {
		return err
	}
	if err := runIn(nsisDir, "makensis", "installer.nsi"); err != nil {
		return fmt.Errorf("makensis 生成安装包失败: %w", err)
	}
	versionedSetup := filepath.Join(nsisDir, fmt.Sprintf("zip233-%s-setup.exe", ver))
	fmt.Printf("NSIS 安装包: %s\n", versionedSetup)

	// Publish both a versioned x64 name (preferred by the updater) and the
	// stable name, each with a strictly matching SHA-256 sidecar.
	for _, name := range []string{
		fmt.Sprintf("zip233-%s-x64-setup.exe", ver),
		"zip233-windows-amd64-setup.exe",
	} {
		alias := filepath.Join(releaseDir, name)
		if err := copyFile(versionedSetup, alias, 0o644); err != nil {
			return fmt.Errorf("生成 NSIS 别名 %s 失败: %w", name, err)
		}
		if err := writeSidecar(releaseDir, alias, name); err != nil {
			return err
		}
	}
	return nil
}

func buildNSISScript(root, releaseDir, ver string) (string, error) {
	gui, err := absNative(filepath.Join(releaseDir, "zip233.exe"))
	if err != nil {
		return "", err
	}
	cli, err := absNative(filepath.Join(releaseDir, "zip233-cli.exe"))
	if err != nil {
		return "", err
	}
	icon, err := absNative(filepath.Join(root, "resources", "icons", "zip233-menu.ico"))
	if err != nil {
		return "", err
	}
	mainIcon, err := absNative(filepath.Join(root, "resources", "icons", "zip233.ico"))
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(`Unicode true

!define APP_VERSION "` + ver + `"
Name "zip233 ${APP_VERSION}"
OutFile "zip233-${APP_VERSION}-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\zip233"
InstallDirRegKey HKCU "Software\neko233\zip233" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "Install"
  SetOutPath "$INSTDIR"
  File "/oname=zip233.exe" "` + gui + `"
  File "/oname=zip233-cli.exe" "` + cli + `"
  File "/oname=zip233.ico" "` + mainIcon + `"
  File "/oname=zip233-menu.ico" "` + icon + `"

  WriteRegStr HKCU "Software\neko233\zip233" "InstallDir" "$INSTDIR"
  WriteRegDWORD HKCU "Software\neko233\zip233" "ShellIntegrationEnabled" 1

  ; Remove verbs owned by older installs.
  DeleteRegKey HKCU "Software\Classes\Directory\shell\zip233.compress"
  DeleteRegKey HKCU "Software\Classes\Directory\Background\shell\zip233.compress-here"
`)

	// File associations: one "zip233.open" verb per supported extension.
	for _, ext := range archiveExtensions {
		for _, verb := range fileVerbs {
			key := `Software\Classes\SystemFileAssociations\.` + ext + `\shell\zip233.` + verb.suffix
			b.WriteString(`  WriteRegStr HKCU "` + key + `" "MUIVerb" "` + verb.muiVerb + `"` + "\n")
			b.WriteString(`  WriteRegStr HKCU "` + key + `" "Icon" "$INSTDIR\zip233-menu.ico"` + "\n")
			b.WriteString(`  WriteRegStr HKCU "` + key + `\command" "" '"$INSTDIR\zip233.exe" ` + verb.command + ` "%1"'` + "\n")
		}
	}
	// Drop stale verbs for formats that are read-only in this version.
	for _, ext := range legacyArchiveExtensions {
		key := `Software\Classes\SystemFileAssociations\.` + ext + `\shell\zip233.open`
		b.WriteString(`  DeleteRegKey HKCU "` + key + `"` + "\n")
	}

	// Directory and directory-background verbs.
	dirVerbs := []struct{ key, label, verb, arg string }{
		{`Software\Classes\Directory\shell\zip233.compress-zip`, `直接压缩为 ZIP`, `compress-zip`, `%1`},
		{`Software\Classes\Directory\shell\zip233.compress-advanced`, `zip233 高级压缩…`, `compress-advanced`, `%1`},
		{`Software\Classes\Directory\Background\shell\zip233.compress-zip-here`, `直接压缩为 ZIP`, `compress-zip-here`, `%V`},
		{`Software\Classes\Directory\Background\shell\zip233.compress-advanced-here`, `zip233 高级压缩…`, `compress-advanced-here`, `%V`},
	}
	for _, v := range dirVerbs {
		b.WriteString(`  WriteRegStr HKCU "` + v.key + `" "MUIVerb" "` + v.label + `"` + "\n")
		b.WriteString(`  WriteRegStr HKCU "` + v.key + `" "Icon" "$INSTDIR\zip233-menu.ico"` + "\n")
		b.WriteString(`  WriteRegStr HKCU "` + v.key + `\command" "" '"$INSTDIR\zip233.exe" ` + v.verb + ` "` + v.arg + `"'` + "\n")
	}

	b.WriteString(`
  ; Start menu shortcuts.
  CreateDirectory "$SMPROGRAMS\zip233"
  CreateShortcut "$SMPROGRAMS\zip233\zip233.lnk" "$INSTDIR\zip233.exe" "" "$INSTDIR\zip233.ico" 0
  CreateShortcut "$SMPROGRAMS\zip233\Uninstall zip233.lnk" "$INSTDIR\uninstall.exe"

  ; Add/Remove Programs (per-user).
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\zip233" "DisplayName" "zip233 ${APP_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\zip233" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\zip233" "Publisher" "neko233-com"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\zip233" "DisplayIcon" "$INSTDIR\zip233.ico"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\zip233" "UninstallString" '"$INSTDIR\uninstall.exe"'

  WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd

Section "Uninstall"
  Delete "$INSTDIR\zip233.exe"
  Delete "$INSTDIR\zip233-cli.exe"
  Delete "$INSTDIR\zip233.ico"
  Delete "$INSTDIR\zip233-menu.ico"
  Delete "$INSTDIR\uninstall.exe"
  Delete "$SMPROGRAMS\zip233\zip233.lnk"
  Delete "$SMPROGRAMS\zip233\Uninstall zip233.lnk"
  RMDir "$SMPROGRAMS\zip233"
`)

	for _, ext := range archiveExtensions {
		for _, verb := range fileVerbs {
			key := `Software\Classes\SystemFileAssociations\.` + ext + `\shell\zip233.` + verb.suffix
			b.WriteString(`  DeleteRegKey HKCU "` + key + `"` + "\n")
		}
	}
	for _, ext := range legacyArchiveExtensions {
		for _, verb := range fileVerbs {
			key := `Software\Classes\SystemFileAssociations\.` + ext + `\shell\zip233.` + verb.suffix
			b.WriteString(`  DeleteRegKey HKCU "` + key + `"` + "\n")
		}
	}
	for _, v := range dirVerbs {
		b.WriteString(`  DeleteRegKey HKCU "` + v.key + `"` + "\n")
	}
	b.WriteString(`  DeleteRegKey HKCU "Software\Classes\Directory\shell\zip233.compress"
  DeleteRegKey HKCU "Software\Classes\Directory\Background\shell\zip233.compress-here"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\zip233"
  DeleteRegValue HKCU "Software\neko233\zip233" "ShellIntegrationEnabled"
  DeleteRegValue HKCU "Software\neko233\zip233" "InstallDir"
  DeleteRegKey HKCU "Software\neko233\zip233\FileAssociations"
  DeleteRegKey HKCU "Software\neko233\zip233\DirectoryVerbs"
  DeleteRegKey HKCU "Software\neko233\zip233\StartMenu"
  DeleteRegKey HKCU "Software\neko233\zip233"
  RMDir /r "$LOCALAPPDATA\zip233\runtime"
  RMDir "$INSTDIR"
SectionEnd
`)
	return b.String(), nil
}

const msiStableName = "zip233-windows-amd64.msi"

// buildMSI produces the canonical stable-named MSI, a versioned alias for the
// in-app updater, and a SHA-256 sidecar. WiX 3 (candle/light) is preferred and
// matches the DownloadX installer; when it is unavailable we fall back to a
// WiX 4/5 `wix build`. The WXS source is always generated so it can be
// inspected even when no WiX tool is installed.
func buildMSI(root, releaseDir, ver string) error {
	msiDir := filepath.Join(releaseDir, "bundle", "msi")
	if err := os.MkdirAll(msiDir, 0o755); err != nil {
		return fmt.Errorf("创建 MSI 目录失败: %w", err)
	}

	payloadDir, err := absSlash(releaseDir)
	if err != nil {
		return err
	}
	icon, err := absSlash(filepath.Join(root, "resources", "icons", "zip233.ico"))
	if err != nil {
		return err
	}
	menuIcon, err := absSlash(filepath.Join(root, "resources", "icons", "zip233-menu.ico"))
	if err != nil {
		return err
	}
	license, err := absSlash(filepath.Join(root, "installer", "License.rtf"))
	if err != nil {
		return err
	}

	versionedName := fmt.Sprintf("zip233-%s-x64.msi", ver)
	stableMSI := filepath.Join(releaseDir, msiStableName)
	versionedMSI := filepath.Join(releaseDir, versionedName)

	if candle, light, ok := findWiX3(); ok {
		wxsPath, err := renderWXS(root, msiDir, "zip233.wxs", ver, payloadDir, icon, menuIcon, license)
		if err != nil {
			return err
		}
		wixobj := filepath.Join(msiDir, "zip233.wixobj")
		fmt.Printf("  使用 WiX 3 编译 MSI: %s\n", wxsPath)
		if err := runIn(root, candle, "-nologo", "-arch", "x64", "-out", wixobj, wxsPath); err != nil {
			return fmt.Errorf("candle 编译失败: %w", err)
		}
		if err := runIn(root, light, "-nologo", "-ext", "WixUIExtension", "-cultures:en-us", "-out", stableMSI, wixobj); err != nil {
			return fmt.Errorf("light 链接失败: %w", err)
		}
	} else if wix, ok := findWixCLI(); ok {
		wxsPath, err := renderWXS(root, msiDir, "zip233-v4.wxs", ver, payloadDir, icon, menuIcon, license)
		if err != nil {
			return err
		}
		fmt.Println("  未找到 WiX 3，改用 wix build（WiX 4/5 回退模板）")
		if err := runIn(root, wix, "build", "-arch", "x64", "-ext", "WixToolset.UI.wixext", "-o", stableMSI, wxsPath); err != nil {
			return fmt.Errorf("wix build 失败: %w", err)
		}
	} else {
		// Still emit the canonical WiX 3 source for local inspection/manual build.
		if _, err := renderWXS(root, msiDir, "zip233.wxs", ver, payloadDir, icon, menuIcon, license); err != nil {
			return err
		}
		return fmt.Errorf("找不到 WiX 3（candle.exe/light.exe）或 wix build；请安装 WiX 3.14 或设置 WIX_BIN 指向含 candle.exe/light.exe 的目录（WXS 已生成，可手动编译）")
	}

	if err := copyFile(stableMSI, versionedMSI, 0o644); err != nil {
		return fmt.Errorf("生成版本化 MSI 别名失败: %w", err)
	}
	if err := writeSidecar(releaseDir, stableMSI, msiStableName); err != nil {
		return err
	}
	if err := writeSidecar(releaseDir, versionedMSI, versionedName); err != nil {
		return err
	}
	fmt.Printf("MSI 安装包: %s\n", stableMSI)
	fmt.Printf("MSI 别名:   %s\n", versionedMSI)
	return nil
}

// findWiX3 locates candle.exe and light.exe via WIX_BIN, PATH, then the common
// WiX 3.14 install directories (matching DownloadX's build-msi.ps1).
func findWiX3() (string, string, bool) {
	if bin := strings.TrimSpace(os.Getenv("WIX_BIN")); bin != "" {
		if candle, light, ok := wix3InDir(bin); ok {
			return candle, light, true
		}
	}
	if candle, err := exec.LookPath("candle"); err == nil {
		if light, err := exec.LookPath("light"); err == nil {
			return candle, light, true
		}
	}
	for _, dir := range []string{
		`C:\Program Files (x86)\WiX Toolset v3.14\bin`,
		`C:\Program Files\WiX Toolset v3.14\bin`,
		`C:\Program Files (x86)\WiX Toolset v3.11\bin`,
	} {
		if candle, light, ok := wix3InDir(dir); ok {
			return candle, light, true
		}
	}
	return "", "", false
}

func wix3InDir(dir string) (string, string, bool) {
	candle := filepath.Join(dir, "candle.exe")
	light := filepath.Join(dir, "light.exe")
	if _, err := os.Stat(candle); err != nil {
		return "", "", false
	}
	if _, err := os.Stat(light); err != nil {
		return "", "", false
	}
	return candle, light, true
}

func findWixCLI() (string, bool) {
	p, err := exec.LookPath("wix")
	if err != nil {
		return "", false
	}
	return p, true
}

func renderWXS(root, msiDir, template, ver, payloadDir, icon, menuIcon, license string) (string, error) {
	tpl, err := os.ReadFile(filepath.Join(root, "installer", template))
	if err != nil {
		return "", fmt.Errorf("读取 WiX 模板 %s 失败: %w", template, err)
	}
	generated := strings.NewReplacer(
		"{{VERSION}}", xmlEscape(ver),
		"{{PAYLOAD_DIR}}", xmlEscape(payloadDir),
		"{{ICON_FILE}}", xmlEscape(icon),
		"{{MENU_ICON_FILE}}", xmlEscape(menuIcon),
		"{{LICENSE_FILE}}", xmlEscape(license),
	).Replace(string(tpl))
	if strings.Contains(generated, "{{") {
		return "", fmt.Errorf("WiX 模板 %s 仍有未替换的占位符", template)
	}
	wxsPath := filepath.Join(msiDir, strings.TrimSuffix(template, ".wxs")+".generated.wxs")
	if err := os.WriteFile(wxsPath, []byte(generated), 0o644); err != nil {
		return "", fmt.Errorf("写入 %s 失败: %w", wxsPath, err)
	}
	fmt.Printf("已生成 WiX 源文件: %s\n", wxsPath)
	return wxsPath, nil
}

// writeSidecar writes a GNU-coreutils-style SHA-256 sidecar whose name is
// strictly "<assetName>.sha256", matching what the installer and updater read.
func writeSidecar(releaseDir, sourcePath, assetName string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("读取安装包 %s 失败: %w", sourcePath, err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	path := filepath.Join(releaseDir, assetName+".sha256")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%s  %s\n", hash, assetName)), 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	fmt.Printf("SHA-256 (%s): %s\n", assetName, hash)
	return nil
}
