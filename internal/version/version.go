// Package version holds build metadata for the vector binary.
package version

import "fmt"

// These are overridden at build time via -ldflags.
var (
	Version = "0.1.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable version string.
func String() string {
	if Commit == "" || Commit == "none" {
		return Version
	}
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, Date)
}
