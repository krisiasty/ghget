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
	results, err := Extract(archivePath, out, "tool.zip", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Written {
		t.Fatalf("results = %+v, want one written file", results)
	}
	got, err := os.ReadFile(filepath.Join(out, "bin", "tool"))
	if err != nil || string(got) != "binary" {
		t.Fatalf("extracted content = %q, err = %v", got, err)
	}
	results, err = Extract(archivePath, out, "tool.zip", Options{})
	if err != nil {
		t.Fatalf("extract identical file: %v", err)
	}
	if len(results) != 1 || results[0].Written {
		t.Fatalf("results = %+v, want one unchanged file", results)
	}
	if err := os.WriteFile(filepath.Join(out, "bin", "tool"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(archivePath, out, "tool.zip", Options{}); err == nil || !strings.Contains(err.Error(), "existing file differs") {
		t.Fatalf("different existing file error = %v", err)
	}
	if _, err := Extract(archivePath, out, "tool.zip", Options{Force: true}); err != nil {
		t.Fatalf("force extraction: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(out, "bin", "tool"))
	if err != nil || string(got) != "binary" {
		t.Fatalf("force-extracted content = %q, err = %v", got, err)
	}
}

func TestExtractTarGzip(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: "docs/README", Mode: 0o644, Size: int64(len(content))}); err != nil {
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
	if _, err := Extract(archivePath, out, "bundle.tar.gz", Options{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "docs", "README"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("extracted content = %q, err = %v", got, err)
	}
	flatOut := t.TempDir()
	if _, err := Extract(archivePath, flatOut, "bundle.tar.gz", Options{Flat: true}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(flatOut, "README"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("flat extracted content = %q, err = %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(flatOut, "docs")); !os.IsNotExist(err) {
		t.Fatalf("nested directory exists after flat extraction: %v", err)
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
	for _, options := range []Options{{}, {Flat: true}} {
		if _, err := Extract(archivePath, t.TempDir(), "evil.zip", options); err == nil {
			t.Fatalf("path traversal unexpectedly accepted with options %+v", options)
		}
	}
}

func TestExtractZIPFlat(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "tool.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range map[string]string{
		"first/bin/tool":  "binary",
		"second/bin/tool": "binary",
		"docs/README":     "documentation",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	results, err := Extract(archivePath, out, "tool.zip", Options{Flat: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	written, unchanged := 0, 0
	for _, result := range results {
		if filepath.Dir(result.Path) != out {
			t.Errorf("flat path = %q, want directory %q", result.Path, out)
		}
		if result.Written {
			written++
		} else {
			unchanged++
		}
	}
	if written != 2 || unchanged != 1 {
		t.Fatalf("results = %+v, want two written and one unchanged", results)
	}
	for name, want := range map[string]string{"tool": "binary", "README": "documentation"} {
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || string(got) != want {
			t.Errorf("%s content = %q, err = %v", name, got, err)
		}
	}
}

func TestExtractFlatCollision(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "collision.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range []struct{ name, content string }{
		{name: "first/tool", content: "first"},
		{name: "second/tool", content: "second"},
	} {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	results, err := Extract(archivePath, out, "collision.zip", Options{Flat: true})
	if err == nil || !strings.Contains(err.Error(), "existing file differs") {
		t.Fatalf("collision error = %v", err)
	}
	if len(results) != 1 || !results[0].Written {
		t.Fatalf("partial results = %+v", results)
	}
	if _, err := Extract(archivePath, out, "collision.zip", Options{Flat: true, Force: true}); err != nil {
		t.Fatalf("force flat extraction: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "tool"))
	if err != nil || string(got) != "second" {
		t.Fatalf("tool content = %q, err = %v", got, err)
	}
}

func TestContentMatchesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		content string
		want    bool
	}{
		{name: "equal", content: "content", want: true},
		{name: "different", content: "contXnt", want: false},
		{name: "shorter", content: "cont", want: false},
		{name: "longer", content: "content-more", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := contentMatchesFile(path, strings.NewReader(test.content))
			if err != nil || got != test.want {
				t.Fatalf("contentMatchesFile() = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}
