package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// archiveExtensions are the file types zip233 owns a "zip233.open" verb for.
// They mirror the extensions registered by the runtime shell integration.
var archiveExtensions = []string{
	"7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz",
	"Z", "taz", "ace", "arj", "arc", "pak", "zoo", "ha", "jar", "j", "uc2",
	"sq", "sq2", "qqq", "sqz", "hyp", "lzh", "lha", "ice", "pi9",
}

// fileVerbs mirror the runtime shell integration for archive files.
var fileVerbs = []struct {
	suffix  string
	muiVerb string
	command string
}{
	{"open", "使用 zip233 打开", "open"},
	{"extract-here", "解压到这里", "extract-here"},
	{"extract-folder", "解压到同名文件夹", "extract-folder"},
	{"extract-to", "解压到文件夹…", "extract-to"},
	{"test", "测试完整性", "test-archive"},
}

// legacyArchiveExtensions are read-only formats that older installs used to
// register. New installers remove those stale verbs instead of adding them.
var legacyArchiveExtensions = []string{
	"zst", "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio",
	"chm", "vhd", "vhdx", "vmdk", "vdi", "qcow2", "udf", "xar", "apfs",
	"squashfs", "cramfs", "fat", "ntfs", "gpt", "mbr", "uefi", "ihex",
	"nsis", "msi", "hfs",
}

// findModuleRoot walks up from the working directory looking for our go.mod.
func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "github.com/neko233-com/zip233") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

// lookTool resolves an external tool and returns a clear Chinese hint when it
// is missing. CI installs makensis and WiX; local users need to do it manually.
func lookTool(tool, hint string) (string, error) {
	path, err := exec.LookPath(tool)
	if err != nil {
		return "", fmt.Errorf("找不到 %s：%s", tool, hint)
	}
	return path, nil
}

func run(name string, args ...string) error {
	return runInEnv("", nil, name, args...)
}

func runIn(dir string, name string, args ...string) error {
	return runInEnv(dir, nil, name, args...)
}

// runInEnv executes a command with live output, optionally in a directory and
// with extra environment variables (GOOS/CGO_ENABLED for cross builds).
func runInEnv(dir string, extraEnv []string, name string, args ...string) error {
	fmt.Printf("  $ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if dir != "" {
		cmd.Dir = dir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s 退出码 %d", name, exitErr.ExitCode())
		}
		return fmt.Errorf("无法执行 %s: %w", name, err)
	}
	return nil
}

// absNative converts a path to an absolute path with OS-native separators.
// NSIS requires backslashes for absolute source paths on Windows.
func absNative(path string) (string, error) {
	return filepath.Abs(path)
}

// absSlash converts a path to an absolute, forward-slash form suitable for
// embedding in WiX XML source paths.
func absSlash(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(abs), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
