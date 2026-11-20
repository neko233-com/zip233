//go:build windows

package shell

import (
	"strings"
	"testing"
)

func TestShellEntries(t *testing.T) {
	entries := shellEntries(`C:\Program Files\zip233\zip233.exe`)
	if len(entries) != 4+len(archiveExtensions)*len(fileVerbs) {
		t.Fatalf("got %d entries, want %d", len(entries), 4+len(archiveExtensions)*len(fileVerbs))
	}

	want := map[string]string{
		`Software\Classes\Directory\shell\zip233.compress-zip`:                      `"C:\Program Files\zip233\zip233.exe" compress-zip "%1"`,
		`Software\Classes\Directory\shell\zip233.compress-advanced`:                 `"C:\Program Files\zip233\zip233.exe" compress-advanced "%1"`,
		`Software\Classes\Directory\Background\shell\zip233.compress-zip-here`:      `"C:\Program Files\zip233\zip233.exe" compress-zip-here "%V"`,
		`Software\Classes\Directory\Background\shell\zip233.compress-advanced-here`: `"C:\Program Files\zip233\zip233.exe" compress-advanced-here "%V"`,
		fileAssocKey("zip"):                  `"C:\Program Files\zip233\zip233.exe" open "%1"`,
		fileVerbKey("zip", "extract-here"):   `"C:\Program Files\zip233\zip233.exe" extract-here "%1"`,
		fileVerbKey("zip", "extract-folder"): `"C:\Program Files\zip233\zip233.exe" extract-folder "%1"`,
		fileVerbKey("zip", "extract-to"):     `"C:\Program Files\zip233\zip233.exe" extract-to "%1"`,
		fileVerbKey("zip", "test"):           `"C:\Program Files\zip233\zip233.exe" test-archive "%1"`,
	}
	for _, e := range entries {
		if expected, ok := want[e.path]; ok && e.command != expected {
			t.Errorf("command for %s = %q, want %q", e.path, e.command, expected)
		}
		if strings.TrimSpace(e.muiVerb) == "" {
			t.Errorf("empty MUIVerb for %s", e.path)
		}
	}
}

func TestIconMatches(t *testing.T) {
	exe := `C:\Program Files\zip233\zip233.exe`
	icon := `C:\Program Files\zip233\zip233-menu.ico`
	if !iconMatches(icon, exe, icon) {
		t.Error("dedicated menu icon should be accepted")
	}
	if !iconMatches(`"`+exe+`"`, exe, icon) {
		t.Error("installer-written exe icon (quoted) should be accepted")
	}
	if iconMatches(`C:\other\tool.exe`, exe, icon) {
		t.Error("unrelated icon must be rejected")
	}
}
