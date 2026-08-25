package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibcForNonLinuxIsNone(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "freebsd"} {
		if got := libcFor(goos, t.TempDir()); got != LibcNone {
			t.Fatalf("libcFor(%q) = %v, want %v", goos, got, LibcNone)
		}
	}
}

func TestLibcForLinuxDefaultsToGlibc(t *testing.T) {
	if got := libcFor("linux", t.TempDir()); got != Glibc {
		t.Fatalf("libcFor() = %v, want %v", got, Glibc)
	}
}

func TestLibcForLinuxDetectsMuslLoader(t *testing.T) {
	root := t.TempDir()
	writeRootFile(t, root, "lib/ld-musl-x86_64.so.1", "")
	if got := libcFor("linux", root); got != Musl {
		t.Fatalf("libcFor() = %v, want %v", got, Musl)
	}
}

func TestLibcForLinuxDetectsAlpineRelease(t *testing.T) {
	root := t.TempDir()
	writeRootFile(t, root, "etc/alpine-release", "3.20.0\n")
	if got := libcFor("linux", root); got != Musl {
		t.Fatalf("libcFor() = %v, want %v", got, Musl)
	}
}

func writeRootFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // The fixture mimics a system directory under a temporary root.
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // The fixture stands in for a system file.
		t.Fatal(err)
	}
}
