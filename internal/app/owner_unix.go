//go:build !windows

package app

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether the effective user owns the file. Root can
// replace any file, and an unrecognized stat type is treated as owned so the
// rename itself reports the problem.
func ownedByCurrentUser(info fs.FileInfo) bool {
	euid := os.Geteuid()
	if euid == 0 {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(stat.Uid) == euid
}
