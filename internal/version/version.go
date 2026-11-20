// Package version is the single source of truth for zip233's version.
package version

// Version is a semantic version without the leading v.
const Version = "0.2.1"

// Tag returns the GitHub release tag for this version.
func Tag() string { return "v" + Version }
