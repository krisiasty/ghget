package archive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestExtractTarZstd(t *testing.T) {
	// ollama publishes its Linux builds only as .tar.zst.
	for _, assetName := range []string{"bundle.tar.zst", "bundle.tzst"} {
		t.Run(assetName, func(t *testing.T) {
			archivePath := writeFileTo(t, assetName, zstdCompress(t, tarWith(t, map[string]string{
				"tool_1.0.0_linux_amd64/tool":      "program",
				"tool_1.0.0_linux_amd64/README.md": "docs",
			})))
			out := t.TempDir()
			if _, err := Extract(archivePath, out, assetName, Options{}); err != nil {
				t.Fatal(err)
			}
			if got := readFrom(t, filepath.Join(out, "tool_1.0.0_linux_amd64", "tool")); got != "program" {
				t.Fatalf("extracted content = %q, want %q", got, "program")
			}
			if got := readFrom(t, filepath.Join(out, "tool_1.0.0_linux_amd64", "README.md")); got != "docs" {
				t.Fatalf("extracted content = %q, want %q", got, "docs")
			}
		})
	}
}

func TestExtractTarZstdFlat(t *testing.T) {
	assetName := "bundle.tar.zst"
	archivePath := writeFileTo(t, assetName, zstdCompress(t, tarWith(t, map[string]string{"docs/README": "hello"})))
	out := t.TempDir()
	if _, err := Extract(archivePath, out, assetName, Options{Flat: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFrom(t, filepath.Join(out, "README")); got != "hello" {
		t.Fatalf("flat extracted content = %q, want %q", got, "hello")
	}
	if _, err := os.Stat(filepath.Join(out, "docs")); !os.IsNotExist(err) {
		t.Fatalf("nested directory exists after flat extraction: %v", err)
	}
}

func TestExtractBareZstd(t *testing.T) {
	// A single zstd-compressed file, named after the asset without its suffix.
	archivePath := writeFileTo(t, "tool.zst", zstdCompress(t, []byte("program")))
	out := t.TempDir()
	results, err := Extract(archivePath, out, "tool.zst", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %v, want one file", results)
	}
	if got := readFrom(t, filepath.Join(out, "tool")); got != "program" {
		t.Fatalf("extracted content = %q, want %q", got, "program")
	}
}

func TestExtractRejectsTruncatedZstd(t *testing.T) {
	compressed := zstdCompress(t, tarWith(t, map[string]string{"tool": "program"}))
	archivePath := writeFileTo(t, "bundle.tar.zst", compressed[:len(compressed)/2])
	if _, err := Extract(archivePath, t.TempDir(), "bundle.tar.zst", Options{}); err == nil {
		t.Fatal("Extract() accepted a truncated archive")
	}
}

func TestExtractRejectsNonZstdContent(t *testing.T) {
	archivePath := writeFileTo(t, "bundle.tar.zst", []byte("this is not compressed"))
	if _, err := Extract(archivePath, t.TempDir(), "bundle.tar.zst", Options{}); err == nil {
		t.Fatal("Extract() accepted content that is not zstd")
	}
}

func TestExtractTarZstdRejectsTraversal(t *testing.T) {
	archivePath := writeFileTo(t, "bundle.tar.zst", zstdCompress(t, tarWith(t, map[string]string{"../escape": "no"})))
	if _, err := Extract(archivePath, t.TempDir(), "bundle.tar.zst", Options{}); err == nil {
		t.Fatal("Extract() accepted an archive escaping the destination")
	}
}

func tarWith(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	for name, content := range entries {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zstdCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	encoder, err := zstd.NewWriter(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeFileTo(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFrom(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
