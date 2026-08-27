package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSelectedZIPMembers(t *testing.T) {
	archivePath := writeSelectedZIP(t, []testZIPEntry{
		{name: "bin/tool", content: "program", mode: 0o755},
		{name: "completions/tool.zsh", content: "completion", mode: 0o644},
		{name: "docs/README.md", content: "documentation", mode: 0o644},
	})
	out := filepath.Join(t.TempDir(), "out")
	results, err := Extract(archivePath, out, "bundle.zip", Options{
		Files: []string{"bin/tool", "completions/tool.zsh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want two files", results)
	}
	for path, want := range map[string]string{
		"bin/tool":             "program",
		"completions/tool.zsh": "completion",
	} {
		if got := readFrom(t, filepath.Join(out, filepath.FromSlash(path))); got != want {
			t.Errorf("%s content = %q, want %q", path, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "docs", "README.md")); !os.IsNotExist(err) {
		t.Fatalf("unselected member exists: %v", err)
	}
	info, err := os.Stat(filepath.Join(out, "completions", "tool.zsh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("selected non-executable mode = %04o, want 0644", got)
	}
}

func TestExtractSelectedTarMembers(t *testing.T) {
	tarData := tarWith(t, map[string]string{
		"bin/tool":       "program",
		"docs/README.md": "documentation",
	})
	formats := []struct {
		assetName string
		data      func(*testing.T, []byte) []byte
	}{
		{assetName: "bundle.tar", data: uncompressed},
		{assetName: "bundle.tar.gz", data: gzipCompress},
		{assetName: "bundle.tgz", data: gzipCompress},
		{assetName: "bundle.tar.zst", data: zstdCompress},
		{assetName: "bundle.tzst", data: zstdCompress},
	}
	for _, format := range formats {
		t.Run(format.assetName, func(t *testing.T) {
			archivePath := writeFileTo(t, format.assetName, format.data(t, tarData))
			out := filepath.Join(t.TempDir(), "out")
			results, err := Extract(archivePath, out, format.assetName, Options{Files: []string{"docs/README.md"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || !results[0].Written {
				t.Fatalf("results = %+v, want one written file", results)
			}
			if got := readFrom(t, filepath.Join(out, "docs", "README.md")); got != "documentation" {
				t.Fatalf("selected content = %q, want %q", got, "documentation")
			}
			if _, err := os.Stat(filepath.Join(out, "bin", "tool")); !os.IsNotExist(err) {
				t.Fatalf("unselected member exists: %v", err)
			}
		})
	}
}

func TestExtractSelectedCompressedFile(t *testing.T) {
	for _, test := range []struct {
		assetName string
		data      []byte
	}{
		{assetName: "tool.gz", data: gzipCompress(t, []byte("gzip program"))},
		{assetName: "tool.zst", data: zstdCompress(t, []byte("zstd program"))},
	} {
		t.Run(test.assetName, func(t *testing.T) {
			archivePath := writeFileTo(t, test.assetName, test.data)
			out := filepath.Join(t.TempDir(), "out")
			if _, err := Extract(archivePath, out, test.assetName, Options{Files: []string{"tool"}}); err != nil {
				t.Fatal(err)
			}
			if got := readFrom(t, filepath.Join(out, "tool")); !strings.HasSuffix(got, " program") {
				t.Fatalf("selected content = %q", got)
			}
		})
	}
}

func TestExtractSelectedMembersValidatesBeforeWriting(t *testing.T) {
	archives := []struct {
		name string
		path func(*testing.T) string
	}{
		{
			name: "ZIP",
			path: func(t *testing.T) string {
				return writeSelectedZIP(t, []testZIPEntry{{name: "present", content: "content", mode: 0o644}})
			},
		},
		{
			name: "TAR",
			path: func(t *testing.T) string {
				return writeFileTo(t, "bundle.tar", tarWith(t, map[string]string{"present": "content"}))
			},
		},
	}
	for _, archive := range archives {
		t.Run(archive.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			_, err := Extract(archive.path(t), out, "bundle."+strings.ToLower(archive.name), Options{
				Files: []string{"present", "missing"},
			})
			if err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("error = %v, want missing member error", err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("destination exists after validation failure: %v", err)
			}
		})
	}
}

func TestExtractSelectedMemberRejectsNonRegularFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		assetName string
		path      string
		member    string
	}{
		{
			name:      "ZIP symlink",
			assetName: "bundle.zip",
			path:      writeSelectedZIP(t, []testZIPEntry{{name: "link", content: "target", mode: os.ModeSymlink | 0o777}}),
			member:    "link",
		},
		{
			name:      "TAR symlink",
			assetName: "bundle.tar",
			path:      writeSelectedTar(t, &tar.Header{Name: "link", Linkname: "target", Mode: 0o777, Typeflag: tar.TypeSymlink}),
			member:    "link",
		},
		{
			name:      "ZIP directory",
			assetName: "bundle.zip",
			path:      writeSelectedZIP(t, []testZIPEntry{{name: "docs/", mode: os.ModeDir | 0o755}}),
			member:    "docs/",
		},
		{
			name:      "TAR directory",
			assetName: "bundle.tar",
			path:      writeSelectedTar(t, &tar.Header{Name: "docs", Mode: 0o755, Typeflag: tar.TypeDir}),
			member:    "docs",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			_, err := Extract(test.path, out, test.assetName, Options{Files: []string{test.member}})
			if err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("error = %v, want non-regular member error", err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("destination exists after validation failure: %v", err)
			}
		})
	}
}

func TestExtractSelectedMembersRejectsUnsafePathAndFlatCollision(t *testing.T) {
	archivePath := writeSelectedZIP(t, []testZIPEntry{
		{name: "first/tool", content: "first", mode: 0o644},
		{name: "second/tool", content: "second", mode: 0o644},
	})
	for _, test := range []struct {
		name    string
		options Options
	}{
		{name: "unsafe path", options: Options{Files: []string{"../outside"}}},
		{name: "flat collision", options: Options{Flat: true, Files: []string{"first/tool", "second/tool"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			if _, err := Extract(archivePath, out, "bundle.zip", test.options); err == nil {
				t.Fatal("Extract() succeeded, want validation error")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("destination exists after validation failure: %v", err)
			}
		})
	}
}

func TestExtractSelectedMembersDeduplicatesRequestsAndHonorsForce(t *testing.T) {
	archivePath := writeSelectedZIP(t, []testZIPEntry{{name: "tool", content: "new", mode: 0o755}})
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "tool"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := Options{Files: []string{"tool", "tool"}}
	if _, err := Extract(archivePath, out, "bundle.zip", options); err == nil {
		t.Fatal("Extract() replaced a differing file without force")
	}
	options.Force = true
	results, err := Extract(archivePath, out, "bundle.zip", options)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Written {
		t.Fatalf("results = %+v, want one written file", results)
	}
	if got := readFrom(t, filepath.Join(out, "tool")); got != "new" {
		t.Fatalf("force-extracted content = %q, want %q", got, "new")
	}
}

type testZIPEntry struct {
	name    string
	content string
	mode    os.FileMode
}

func writeSelectedZIP(t *testing.T, entries []testZIPEntry) string {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFileTo(t, "bundle.zip", buffer.Bytes())
}

func writeSelectedTar(t *testing.T, header *tar.Header) string {
	t.Helper()
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	if err := tw.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFileTo(t, "bundle.tar", buffer.Bytes())
}

func uncompressed(_ *testing.T, data []byte) []byte {
	return data
}

func gzipCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
