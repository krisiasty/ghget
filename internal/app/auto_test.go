package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	gh "github.com/krisiasty/ghget/internal/github"
	"github.com/krisiasty/ghget/internal/platform"
)

var linuxAMD64 = platform.Platform{OS: "linux", Arch: "amd64", Libc: platform.Glibc}

func TestAutoSelectsTheAssetForThisPlatform(t *testing.T) {
	client := &fakeClient{
		tag: "v1.0.0",
		assets: assetsNamed(
			"tool_1.0.0_checksums.txt",
			"tool_1.0.0_darwin_arm64.tar.gz",
			"tool_1.0.0_linux_amd64.tar.gz",
			"tool_1.0.0_linux_amd64.deb",
			"tool_1.0.0_linux_arm64.tar.gz",
			"tool_1.0.0_windows_amd64.zip",
		),
		content: map[string]string{
			"tool_1.0.0_linux_amd64.tar.gz": "archive bytes",
			"tool_1.0.0_checksums.txt":      checksumLine("archive bytes", "tool_1.0.0_linux_amd64.tar.gz"),
		},
	}
	directory := t.TempDir()
	var stderr strings.Builder
	app := newTestApp(client, io.Discard, &stderr, linuxAMD64)

	if err := app.Run(context.Background(), []string{"acme/tool", "--auto", "--dir", directory}); err != nil {
		t.Fatal(err)
	}

	// The checksum manifest is fetched too; no other platform's asset should be.
	if !slices.Contains(client.downloads, "tool_1.0.0_linux_amd64.tar.gz") {
		t.Fatalf("downloaded %v, want the linux/amd64 archive", client.downloads)
	}
	for _, name := range client.downloads {
		if strings.Contains(name, "darwin") || strings.Contains(name, "windows") || strings.Contains(name, "arm64") {
			t.Fatalf("downloaded %q, want only this platform's asset", name)
		}
	}
	if content := readIfExists(t, filepath.Join(directory, "tool_1.0.0_linux_amd64.tar.gz")); content != "archive bytes" {
		t.Fatalf("saved content = %q, want the downloaded archive", content)
	}
	if !strings.Contains(stderr.String(), "selected tool_1.0.0_linux_amd64.tar.gz") {
		t.Fatalf("stderr = %q, want it to report the selection", stderr.String())
	}
}

func TestAutoInstallPlacesOnlyProgramsFromAnArchive(t *testing.T) {
	archive := tarGzip(t, map[string]tarEntry{
		"tool_1.0.0_linux_amd64/tool":                  {mode: 0o755, content: "program"},
		"tool_1.0.0_linux_amd64/README.md":             {mode: 0o644, content: "docs"},
		"tool_1.0.0_linux_amd64/completions/tool.bash": {mode: 0o644, content: "completions"},
	})
	client := &fakeClient{
		tag:     "v1.0.0",
		assets:  assetsNamed("tool_1.0.0_linux_amd64.tar.gz", "tool_1.0.0_windows_amd64.zip"),
		content: map[string]string{"tool_1.0.0_linux_amd64.tar.gz": archive},
	}
	directory := t.TempDir()
	app := newTestApp(client, io.Discard, io.Discard, linuxAMD64)

	if err := app.Run(context.Background(), []string{"acme/tool", "--auto", "--install", "--dir", directory}); err != nil {
		t.Fatal(err)
	}

	if got := entryNames(t, directory); strings.Join(got, ",") != "tool" {
		t.Fatalf("destination holds %v, want only the program", got)
	}
	if content := readIfExists(t, filepath.Join(directory, "tool")); content != "program" {
		t.Fatalf("installed content = %q, want the program", content)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(directory, "tool"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("installed mode = %v, want 0755", info.Mode().Perm())
		}
	}
}

func TestAutoInstallNamesABareBinaryAfterItsProgram(t *testing.T) {
	client := &fakeClient{
		tag:    "v1.0.0",
		assets: assetsNamed("tool-linux-amd64", "tool-darwin-arm64", "tool-linux-amd64.sha256"),
		content: map[string]string{
			"tool-linux-amd64":        "program",
			"tool-linux-amd64.sha256": digest("program"),
		},
	}
	directory := t.TempDir()
	app := newTestApp(client, io.Discard, io.Discard, linuxAMD64)

	if err := app.Run(context.Background(), []string{"acme/tool", "--auto", "--install", "--dir", directory}); err != nil {
		t.Fatal(err)
	}

	if got := entryNames(t, directory); strings.Join(got, ",") != "tool" {
		t.Fatalf("destination holds %v, want the program named \"tool\"", got)
	}
}

func TestInstallBareBinaryDoesNotUseSystemTemp(t *testing.T) {
	downloadDirectory := t.TempDir()
	downloaded := filepath.Join(downloadDirectory, ".ghget-download")
	if err := os.WriteFile(downloaded, []byte("program"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(downloadDirectory, "bin")

	missingTemp := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missingTemp)
	t.Setenv("TMP", missingTemp)
	t.Setenv("TEMP", missingTemp)
	if filepath.Clean(os.TempDir()) != filepath.Clean(missingTemp) {
		t.Skipf("cannot redirect the system temporary directory on %s", runtime.GOOS)
	}

	app := newTestApp(&fakeClient{}, io.Discard, io.Discard, linuxAMD64)
	paths, err := app.installAsset(downloaded, gh.Asset{Name: "tool-linux-amd64"}, options{directory: destination})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(destination, "tool")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("installAsset() = %v, want [%s]", paths, want)
	}
	if content := readIfExists(t, want); content != "program" {
		t.Fatalf("installed content = %q, want the program", content)
	}
}

func TestAutoInstallKeepsTheArchiveWhenAsked(t *testing.T) {
	archive := tarGzip(t, map[string]tarEntry{"tool": {mode: 0o755, content: "program"}})
	client := &fakeClient{
		tag:     "v1.0.0",
		assets:  assetsNamed("tool_1.0.0_linux_amd64.tar.gz"),
		content: map[string]string{"tool_1.0.0_linux_amd64.tar.gz": archive},
	}
	directory := t.TempDir()
	app := newTestApp(client, io.Discard, io.Discard, linuxAMD64)

	args := []string{"acme/tool", "--auto", "--install", "--keep", "--dir", directory}
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}

	want := "tool,tool_1.0.0_linux_amd64.tar.gz"
	if got := entryNames(t, directory); strings.Join(got, ",") != want {
		t.Fatalf("destination holds %v, want %q", got, want)
	}
}

func TestAutoStopsWhenCandidatesTie(t *testing.T) {
	client := &fakeClient{
		tag:     "v1.0.0",
		assets:  assetsNamed("tool-macos-amd64", "tool-osx-amd64"),
		content: map[string]string{"tool-macos-amd64": "program", "tool-osx-amd64": "program"},
	}
	darwin := platform.Platform{OS: "darwin", Arch: "amd64"}
	var stderr strings.Builder
	app := newTestApp(client, io.Discard, &stderr, darwin)

	err := app.Run(context.Background(), []string{"acme/tool", "--auto", "--dir", t.TempDir()})
	if err == nil {
		t.Fatal("Run() succeeded, want an ambiguity error")
	}
	if len(client.downloads) != 0 {
		t.Fatalf("downloaded %v, want nothing downloaded when ambiguous", client.downloads)
	}
	for _, name := range []string{"tool-macos-amd64", "tool-osx-amd64", "--first"} {
		if !strings.Contains(err.Error()+stderr.String(), name) {
			t.Fatalf("output = %q / %q, want it to mention %q", err, stderr.String(), name)
		}
	}
}

func TestAutoFirstResolvesATie(t *testing.T) {
	client := &fakeClient{
		tag:     "v1.0.0",
		assets:  assetsNamed("tool-macos-amd64", "tool-osx-amd64"),
		content: map[string]string{"tool-macos-amd64": "program", "tool-osx-amd64": "program"},
	}
	darwin := platform.Platform{OS: "darwin", Arch: "amd64"}
	app := newTestApp(client, io.Discard, io.Discard, darwin)

	args := []string{"acme/tool", "--auto", "--first", "--dir", t.TempDir()}
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(client.downloads, ","); got != "tool-macos-amd64" {
		t.Fatalf("downloaded %q, want the top-ranked candidate", got)
	}
}

func TestAutoReportsWhenNothingTargetsThisPlatform(t *testing.T) {
	client := &fakeClient{
		tag:    "v1.0.0",
		assets: assetsNamed("tool_1.0.0_windows_amd64.zip", "tool_1.0.0_darwin_arm64.tar.gz"),
	}
	var stderr strings.Builder
	app := newTestApp(client, io.Discard, &stderr, linuxAMD64)

	err := app.Run(context.Background(), []string{"acme/tool", "--auto", "--dir", t.TempDir()})
	if err == nil {
		t.Fatal("Run() succeeded, want a no-match error")
	}
	if !strings.Contains(err.Error(), "linux/amd64") {
		t.Fatalf("error = %q, want it to name the platform", err)
	}
	if !strings.Contains(err.Error(), "--list") {
		t.Fatalf("error = %q, want it to suggest --list", err)
	}
	if !strings.Contains(stderr.String(), "built for windows") {
		t.Fatalf("stderr = %q, want it to explain each rejection", stderr.String())
	}
}

func TestAutoRejectsAFilePattern(t *testing.T) {
	app := newTestApp(&fakeClient{tag: "v1.0.0"}, io.Discard, io.Discard, linuxAMD64)
	err := app.Run(context.Background(), []string{"acme/tool/tool-linux-amd64", "--auto"})
	if err == nil || !strings.Contains(err.Error(), "--auto") {
		t.Fatalf("Run() error = %v, want --auto to reject a file pattern", err)
	}
}

func TestInstallReportsArchivesWithoutPrograms(t *testing.T) {
	archive := tarGzip(t, map[string]tarEntry{"docs/README.md": {mode: 0o644, content: "docs"}})
	client := &fakeClient{
		tag:     "v1.0.0",
		assets:  assetsNamed("tool_1.0.0_linux_amd64.tar.gz"),
		content: map[string]string{"tool_1.0.0_linux_amd64.tar.gz": archive},
	}
	app := newTestApp(client, io.Discard, io.Discard, linuxAMD64)

	args := []string{"acme/tool", "--auto", "--install", "--dir", t.TempDir()}
	err := app.Run(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("Run() error = %v, want it to report that no program was found", err)
	}
}

func newTestApp(client releaseClient, stdout, stderr io.Writer, target platform.Platform) *App {
	app := NewWithClient(client, stdout, stderr)
	app.platform = target
	return app
}

func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func checksumLine(content, name string) string {
	return digest(content) + "  " + name + "\n"
}

func assetsNamed(names ...string) []gh.Asset {
	assets := make([]gh.Asset, 0, len(names))
	for _, name := range names {
		assets = append(assets, gh.Asset{Name: name})
	}
	return assets
}

type tarEntry struct {
	mode    fs.FileMode
	content string
}

func tarGzip(t *testing.T, entries map[string]tarEntry) string {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for name, entry := range entries {
		header := &tar.Header{
			Name:     name,
			Mode:     int64(entry.mode),
			Size:     int64(len(entry.content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}

func entryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func readIfExists(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
