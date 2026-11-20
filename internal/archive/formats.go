// Package archive implements zip233's archive reading, creation and extraction
// engine on top of mholt/archives and the Go standard library.
package archive

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ArchiveExtensions are the archive/compression extensions zip233 recognizes.
// Note: Go has no constant slices, so these are vars with fixed contents.
var ArchiveExtensions = []string{
	"7z", "zip", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz",
	"Z", "taz", "ace", "arj", "arc", "pak", "zoo", "ha", "jar", "j", "uc2",
	"sq", "sq2", "qqq", "sqz", "hyp", "lzh", "lha", "ice", "pi9",
}

// LegacyArchiveExtensions are legacy / disk-image formats that are recognized
// but not all of them are supported for reading or writing.
var LegacyArchiveExtensions = []string{
	"zst", "lz", "lzma", "cab", "wim", "iso", "dmg", "rpm", "deb", "cpio",
	"chm", "vhd", "vhdx", "vmdk", "vdi", "qcow2", "udf", "xar", "apfs",
	"squashfs", "cramfs", "fat", "ntfs", "gpt", "mbr", "uefi", "ihex", "nsis", "msi", "hfs",
}

var createFormats = []string{"7z", "zip", "tar", "tar.gz", "tar.xz", "tar.bz2", "gz", "bz2", "xz"}

var readFormats = []string{"7z", "zip", "rar", "tar", "tar.gz", "tar.xz", "tar.bz2", "tar.zst", "gz", "bz2", "xz", "zst"}

// SupportedCreateFormats returns the format identifiers accepted by Create.
func SupportedCreateFormats() []string { return append([]string(nil), createFormats...) }

// SupportedReadFormats returns the format identifiers the engine can read.
func SupportedReadFormats() []string { return append([]string(nil), readFormats...) }

// IsSupportedCreateFormat reports whether format is a create format.
func IsSupportedCreateFormat(format string) bool {
	f := normalizeFormat(format)
	for _, item := range createFormats {
		if f == item {
			return true
		}
	}
	return false
}

// normalizeFormat canonicalizes user-supplied format strings.
func normalizeFormat(format string) string {
	f := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(format), ".")))
	switch f {
	case "tgz":
		return "tar.gz"
	case "tbz", "tbz2":
		return "tar.bz2"
	case "txz":
		return "tar.xz"
	case "taz":
		return "tar.gz"
	}
	return f
}

// formatFromExtension guesses a user-facing format identifier from a target path.
func formatFromExtension(target string) string {
	base := strings.ToLower(filepath.Base(target))
	switch {
	case strings.HasSuffix(base, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(base, ".tar.bz2"):
		return "tar.bz2"
	case strings.HasSuffix(base, ".tar.xz"):
		return "tar.xz"
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	return normalizeFormat(ext)
}

// CompressionLevel is the numeric level used by deflate/flate/bzip2/xz writers.
// -1 means "library default", 0 means "store/no compression".
type CompressionLevel struct {
	Value int
	Valid bool
}

// parseLevel parses a --level value ("none", "fast", "best" or 0-9).
func parseLevel(level string) (CompressionLevel, error) {
	s := strings.ToLower(strings.TrimSpace(level))
	if s == "" {
		return CompressionLevel{Value: -1, Valid: false}, nil
	}
	switch s {
	case "none", "store", "0":
		return CompressionLevel{Value: 0, Valid: true}, nil
	case "fast", "1":
		return CompressionLevel{Value: 1, Valid: true}, nil
	case "best", "9":
		return CompressionLevel{Value: 9, Valid: true}, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 9 {
		return CompressionLevel{}, fmt.Errorf("无效的压缩级别 %q：请使用 none/fast/best 或 0-9", level)
	}
	return CompressionLevel{Value: n, Valid: true}, nil
}
