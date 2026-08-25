package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	gh "github.com/krisiasty/ghget/internal/github"
)

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		value string
		want  string
	}{
		{value: "~", want: home},
		{value: "~/downloads/tools", want: filepath.Join(home, "downloads", "tools")},
		{value: "$HOME", want: home},
		{value: "$HOME/downloads", want: filepath.Join(home, "downloads")},
		{value: "${HOME}", want: home},
		{value: "${HOME}/downloads", want: filepath.Join(home, "downloads")},
		{value: "~someone/downloads", want: "~someone/downloads"},
		{value: "$HOME-backup", want: "$HOME-backup"},
		{value: "relative/$HOME", want: "relative/$HOME"},
	}
	for _, test := range tests {
		got, err := expandHomePath(test.value)
		if err != nil {
			t.Errorf("expandHomePath(%q): %v", test.value, err)
			continue
		}
		if got != test.want {
			t.Errorf("expandHomePath(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestParseOptionsExpandsHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	opts, err := parseOptions([]string{"acme/tool/file", "--dir=$HOME/downloads"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "downloads"); opts.directory != want {
		t.Fatalf("directory = %q, want %q", opts.directory, want)
	}
}

func TestRunCreatesMissingDestinationDirectory(t *testing.T) {
	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "tool"}},
		content: map[string]string{"tool": "binary"},
	}
	destination := filepath.Join(t.TempDir(), "missing", "nested")
	if err := NewWithClient(client, io.Discard, io.Discard).Run(context.Background(), []string{
		"acme/tool/tool", "--dir", destination,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "tool")) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(got) != "binary" {
		t.Fatalf("downloaded content = %q, err = %v", got, err)
	}
}
