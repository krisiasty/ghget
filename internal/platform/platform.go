// Package platform resolves the host OS, architecture, and C library so that
// release assets can be matched against the machine that will run them.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// Libc identifies the C library a Linux host provides.
type Libc int

const (
	// LibcNone is used by platforms where the C library does not vary.
	LibcNone Libc = iota
	// Glibc is the GNU C library, the default on mainstream distributions.
	Glibc
	// Musl is the musl C library, used by Alpine and similar distributions.
	Musl
)

// String reports the token conventionally used for the C library in asset names.
func (l Libc) String() string {
	switch l {
	case Glibc:
		return "gnu"
	case Musl:
		return "musl"
	default:
		return ""
	}
}

// Platform describes the machine an asset must run on.
type Platform struct {
	OS   string
	Arch string
	Libc Libc
}

// Detect reports the platform of the running host.
func Detect() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Libc: libcFor(runtime.GOOS, "/")}
}

// libcFor reports the C library under root, which is the filesystem root except in tests.
func libcFor(goos, root string) Libc {
	if goos != "linux" {
		return LibcNone
	}
	if matches, err := filepath.Glob(filepath.Join(root, "lib", "ld-musl-*.so.1")); err == nil && len(matches) > 0 {
		return Musl
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "alpine-release")); err == nil {
		return Musl
	}
	return Glibc
}
