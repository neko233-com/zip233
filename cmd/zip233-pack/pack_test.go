package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testVersion = "9.9.9-test"

func TestBuildNSISScriptContainsAllVerbs(t *testing.T) {
	root := t.TempDir()
	releaseDir := t.TempDir()
	script, err := buildNSISScript(root, releaseDir, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "{{") {
		t.Fatal("NSIS 脚本不应残留模板占位符")
	}
	if !strings.Contains(script, `!define APP_VERSION "`+testVersion+`"`) {
		t.Error("版本号未写入 NSIS 脚本")
	}
	// 输出名必须稳定，供 CI/发布流程精确匹配。
	if !strings.Contains(script, `OutFile "zip233-${APP_VERSION}-setup.exe"`) {
		t.Error("NSIS 输出名不稳定")
	}

	for _, ext := range archiveExtensions {
		for _, verb := range fileVerbs {
			key := `Software\Classes\SystemFileAssociations\.` + ext + `\shell\zip233.` + verb.suffix
			if !strings.Contains(script, key) {
				t.Errorf("NSIS 缺少扩展名 %s 的 %s 动词", ext, verb.suffix)
			}
		}
	}
	for _, ext := range legacyArchiveExtensions {
		key := `Software\Classes\SystemFileAssociations\.` + ext + `\shell\zip233.open`
		if !strings.Contains(script, key) {
			t.Errorf("NSIS 缺少旧版 %s 的清理项", ext)
		}
	}
	for _, fragment := range []string{
		"compress-zip", "compress-zip-here", "compress-advanced", "compress-advanced-here",
		`"MUIVerb"`, `%1`, `%V`, "$INSTDIR\\zip233-menu.ico",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("NSIS 缺少片段 %q", fragment)
		}
	}
}

func TestBuildNSISScriptDeterministic(t *testing.T) {
	root := t.TempDir()
	releaseDir := t.TempDir()
	a, err := buildNSISScript(root, releaseDir, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildNSISScript(root, releaseDir, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("同样的输入必须生成字节一致的 NSIS 脚本")
	}
}

func TestRenderWXSTemplates(t *testing.T) {
	root := findModuleRoot()
	msiDir := t.TempDir()
	// 与 buildMSI 一致，传入 absSlash 风格的路径。
	payloadDir := filepath.ToSlash(filepath.Join(t.TempDir(), "payload"))
	icon := filepath.ToSlash(filepath.Join(root, "resources", "icons", "zip233.ico"))
	menuIcon := filepath.ToSlash(filepath.Join(root, "resources", "icons", "zip233-menu.ico"))
	license := filepath.ToSlash(filepath.Join(root, "installer", "License.rtf"))

	// WiX 3 主模板与 WiX 4/5 回退模板都必须能干净渲染。
	for _, template := range []string{"zip233.wxs", "zip233-v4.wxs"} {
		t.Run(template, func(t *testing.T) {
			path, err := renderWXS(root, msiDir, template, testVersion, payloadDir, icon, menuIcon, license)
			if err != nil {
				t.Fatalf("renderWXS(%s) 失败：%v", template, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			generated := string(data)
			if strings.Contains(generated, "{{") {
				t.Fatalf("WXS 生成物残留占位符：%s", remainingPlaceholders(generated))
			}
			if !strings.Contains(generated, testVersion) ||
				!strings.Contains(generated, xmlEscape(payloadDir)) ||
				!strings.Contains(generated, xmlEscape(icon)) {
				t.Error("WXS 生成物缺少替换后的版本号或路径")
			}
			// MSI 不直接写右键动词；由 runtime internal/shell 注册并自修复。
			if !strings.Contains(generated, "ShellIntegrationEnabled") {
				t.Error("WXS 应写入运行时右键集成开关")
			}
			if strings.Contains(generated, "SystemFileAssociations") ||
				strings.Contains(generated, `Directory\shell`) {
				t.Error("WXS 不应写右键动词，避免与 per-user 运行时注册冲突")
			}
			if got := strings.Count(generated, "Guid="); got != 6 {
				t.Errorf("应有 6 个固定 GUID 组件，实际 %d", got)
			}
			if got := strings.Count(generated, "<File "); got != 4 {
				t.Errorf("应有 4 个安装文件，实际 %d", got)
			}
			for _, file := range []string{"zip233.exe", "zip233-cli.exe", "zip233.ico", "zip233-menu.ico"} {
				if !strings.Contains(generated, file) {
					t.Errorf("WXS 缺少安装文件 %s", file)
				}
			}
		})
	}
}

func TestStableReleaseNames(t *testing.T) {
	if msiStableName != "zip233-windows-amd64.msi" {
		t.Fatalf("MSI 稳定输出名变更：%q", msiStableName)
	}
	dir := t.TempDir()
	artifact := filepath.Join(dir, msiStableName)
	payload := []byte("installer bytes")
	if err := os.WriteFile(artifact, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSidecar(dir, artifact, msiStableName); err != nil {
		t.Fatalf("writeSidecar 失败：%v", err)
	}
	sidecar := filepath.Join(dir, msiStableName+".sha256")
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("未生成稳定的 sidecar 文件名：%v", err)
	}
	sum := sha256.Sum256(payload)
	if !strings.HasPrefix(string(data), hex.EncodeToString(sum[:])) ||
		!strings.Contains(string(data), msiStableName) {
		t.Fatalf("sidecar 内容错误：%q", data)
	}
}

func remainingPlaceholders(text string) string {
	seen := map[string]bool{}
	var out []string
	for rest := text; ; {
		i := strings.Index(rest, "{{")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], "}}")
		if j < 0 {
			break
		}
		token := rest[i : i+j+2]
		if !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
		rest = rest[i+j+2:]
	}
	return strings.Join(out, ", ")
}

func TestXMLHelpers(t *testing.T) {
	if got := xmlEscape(`a&b<c>"d" 'e'`); got != "a&amp;b&lt;c&gt;&quot;d&quot; &apos;e&apos;" {
		t.Errorf("xmlEscape = %q", got)
	}
	if err := joinErrors(nil, nil); err != nil {
		t.Errorf("joinErrors(nil,nil) = %v", err)
	}
	if err := joinErrors(os.ErrNotExist); err == nil {
		t.Error("joinErrors 应保留非空错误")
	}
	if root := findModuleRoot(); !strings.Contains(readFileString(t, filepath.Join(root, "go.mod")), "github.com/neko233-com/zip233") {
		t.Errorf("findModuleRoot 返回错误目录：%s", root)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
