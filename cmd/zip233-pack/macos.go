package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// bundleMacOS assembles a native .app bundle and compresses it into a DMG.
func bundleMacOS(root, releaseDir, ver string) error {
	macDir := filepath.Join(releaseDir, "bundle", "macos")
	appDir := filepath.Join(macDir, "zip233.app")
	macOSDir := filepath.Join(appDir, "Contents", "MacOS")
	resDir := filepath.Join(appDir, "Contents", "Resources")

	for _, dir := range []string{macOSDir, resDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建 app 目录失败: %w", err)
		}
	}

	gui := filepath.Join(releaseDir, "zip233")
	cli := filepath.Join(releaseDir, "zip233-cli")
	icon := filepath.Join(root, "resources", "icons", "zip233.icns")
	for _, input := range []string{gui, cli, icon} {
		if _, err := os.Stat(input); err != nil {
			return fmt.Errorf("缺少打包输入 %s: %w", input, err)
		}
	}

	if err := copyFile(gui, filepath.Join(macOSDir, "zip233"), 0o755); err != nil {
		return fmt.Errorf("复制 GUI 失败: %w", err)
	}
	if err := copyFile(cli, filepath.Join(macOSDir, "zip233-cli"), 0o755); err != nil {
		return fmt.Errorf("复制 CLI 失败: %w", err)
	}
	if err := copyFile(icon, filepath.Join(resDir, "zip233.icns"), 0o644); err != nil {
		return fmt.Errorf("复制图标失败: %w", err)
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>zh_CN</string>
  <key>CFBundleExecutable</key><string>zip233</string>
  <key>CFBundleIdentifier</key><string>com.neko233.zip233</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>zip233</string>
  <key>CFBundleDisplayName</key><string>zip233</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>%s</string>
  <key>CFBundleVersion</key><string>%s</string>
  <key>CFBundleIconFile</key><string>zip233.icns</string>
  <key>LSMinimumSystemVersion</key><string>10.15</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
`, xmlEscape(ver), xmlEscape(ver))
	plistPath := filepath.Join(appDir, "Contents", "Info.plist")
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("写入 Info.plist 失败: %w", err)
	}
	fmt.Printf("已生成 macOS app: %s\n", appDir)

	if _, err := lookTool("hdiutil", "hdiutil 是 macOS 自带工具，请在 macOS 主机上运行本打包器"); err != nil {
		return err
	}
	dmg := filepath.Join(macDir, fmt.Sprintf("zip233-%s.dmg", ver))
	// Remove a previous artifact so -ov never prompts.
	_ = os.Remove(dmg)
	if err := runIn(root, "hdiutil", "create", "-volname", "zip233", "-srcfolder", appDir, "-ov", "-format", "UDZO", dmg); err != nil {
		return fmt.Errorf("hdiutil 生成 DMG 失败: %w", err)
	}
	fmt.Printf("DMG 安装包: %s\n", dmg)
	return nil
}
