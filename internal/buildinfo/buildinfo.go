// Package buildinfo formats version metadata injected at link time.
package buildinfo

var (
	version = "dev"
	commit  = "unknown"
	built   = "unknown"
)

// String returns ghget's version, commit, and build timestamp.
func String() string {
	return "ghget " + version + " (commit " + commit + ", built " + built + ")"
}
