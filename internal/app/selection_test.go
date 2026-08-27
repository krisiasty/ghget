package app

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	gh "github.com/krisiasty/ghget/internal/github"
)

func TestRunExtractsSelectedArchiveMembers(t *testing.T) {
	var archiveContent bytes.Buffer
	zw := zip.NewWriter(&archiveContent)
	for name, content := range map[string]string{
		"bin/tool":             "program",
		"completions/tool.zsh": "completion",
		"docs/README.md":       "documentation",
	} {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "bundle.zip", URL: "asset:bundle.zip"}},
		content: map[string]string{"bundle.zip": archiveContent.String()},
	}
	out := filepath.Join(t.TempDir(), "out")
	err := NewWithClient(client, io.Discard, io.Discard).Run(context.Background(), []string{
		"acme/tool/bundle.zip",
		"--dir", out,
		"--extract",
		"--file", "bin/tool",
		"--file", "completions/tool.zsh",
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(out, "bin", "tool"):             "program",
		filepath.Join(out, "completions", "tool.zsh"): "completion",
	} {
		content, err := os.ReadFile(path) //nolint:gosec // The path is confined to the test's temporary directory.
		if err != nil || string(content) != want {
			t.Errorf("%s content = %q, err = %v; want %q", path, content, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "docs", "README.md")); !os.IsNotExist(err) {
		t.Fatalf("unselected member exists: %v", err)
	}
}
