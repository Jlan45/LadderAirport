// Package version holds build-time identity for the Panel binary.
// Values are overridden via -ldflags, e.g.:
//
//	-X github.com/ladderairport/panel/internal/version.Version=v0.3.1
package version

// Version is the panel product version (git tag or "0.1.0-dev").
var Version = "0.1.0-dev"

// Commit is the short git commit when injected at build time.
var Commit = "unknown"

// BuiltAt is a build timestamp when injected at build time.
var BuiltAt = "unknown"
