package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlaceCopiesProgramsIntoDestination(t *testing.T) {
	source := buildTree(t, map[string]fs.FileMode{
		"uv-x86_64-apple-darwin/uv":  0o755,
		"uv-x86_64-apple-darwin/uvx": 0o755,
	})
	destination := filepath.Join(t.TempDir(), "missing", "bin")
	programs, err := Executables(source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}

	placed, err := Place(programs, destination, false)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{filepath.Join(destination, "uv"), filepath.Join(destination, "uvx")}
	if strings.Join(placed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Place() = %v, want %v", placed, want)
	}
	for _, path := range placed {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
			t.Fatalf("%s has mode %v, want 0755", path, info.Mode().Perm())
		}
	}
}

func TestPlaceAsUsesTheExactTarget(t *testing.T) {
	source := buildTree(t, map[string]fs.FileMode{"tool": 0o755})
	destination := filepath.Join(t.TempDir(), "missing", "bin")
	target := filepath.Join(destination, "renamed")

	if err := PlaceAs(filepath.Join(source, "tool"), target, false); err != nil {
		t.Fatal(err)
	}

	if content := readFile(t, target); content != "tool" {
		t.Fatalf("target content = %q, want the program", content)
	}
	if _, err := os.Stat(filepath.Join(destination, "tool")); !os.IsNotExist(err) {
		t.Fatalf("natural-name target exists or cannot be checked: %v", err)
	}
}

func TestPlaceKeepsAnIdenticalExistingFile(t *testing.T) {
	source := buildTree(t, map[string]fs.FileMode{"tool": 0o755})
	destination := t.TempDir()
	writeFile(t, filepath.Join(destination, "tool"), "tool")

	if _, err := Place([]string{filepath.Join(source, "tool")}, destination, false); err != nil {
		t.Fatalf("Place() error = %v, want an identical file to be accepted", err)
	}
}

func TestPlaceRefusesToReplaceADifferentFile(t *testing.T) {
	source := buildTree(t, map[string]fs.FileMode{"tool": 0o755})
	destination := t.TempDir()
	writeFile(t, filepath.Join(destination, "tool"), "something else")

	_, err := Place([]string{filepath.Join(source, "tool")}, destination, false)
	if err == nil {
		t.Fatal("Place() replaced a differing file without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("Place() error = %v, want it to suggest --force", err)
	}
	if content := readFile(t, filepath.Join(destination, "tool")); content != "something else" {
		t.Fatalf("destination content = %q, want it left alone", content)
	}
}

func TestPlaceOverwritesWithForce(t *testing.T) {
	source := buildTree(t, map[string]fs.FileMode{"tool": 0o755})
	destination := t.TempDir()
	writeFile(t, filepath.Join(destination, "tool"), "something else")

	if _, err := Place([]string{filepath.Join(source, "tool")}, destination, true); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, filepath.Join(destination, "tool")); content != "tool" {
		t.Fatalf("destination content = %q, want it replaced", content)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // The fixture stands in for a file already in the destination.
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
