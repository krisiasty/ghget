package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZIP(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "tool.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("bin/tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	paths, err := Extract(archivePath, out, "tool.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	got, err := os.ReadFile(filepath.Join(out, "bin", "tool"))
	if err != nil || string(got) != "binary" {
		t.Fatalf("extracted content = %q, err = %v", got, err)
	}
}

func TestExtractTarGzip(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: "README", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(archivePath, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := Extract(archivePath, out, "bundle.tar.gz"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "README"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("extracted content = %q, err = %v", got, err)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../outside")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(archivePath, t.TempDir(), "evil.zip"); err == nil {
		t.Fatal("path traversal unexpectedly accepted")
	}
}
