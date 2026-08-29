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
		{value: "./downloads", want: "./downloads"},
		{value: "a/downloads", want: "a/downloads"},
		{value: "..", want: ".."},
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

func TestParseOptionsUpgrade(t *testing.T) {
	opts, err := parseOptions([]string{"--upgrade", "--skip-version-check"})
	if err != nil {
		t.Fatalf("parseOptions(--upgrade): %v", err)
	}
	if !opts.upgrade {
		t.Fatal("upgrade = false, want true")
	}
	if !opts.skipVersionCheck {
		t.Fatal("skipVersionCheck = false, want true")
	}
	for _, args := range [][]string{
		{"--upgrade", "acme/tool"},
		{"--upgrade", "--dir", "/tmp"},
		{"--upgrade", "--output", "ghget"},
		{"--upgrade", "--extract"},
		{"--upgrade", "--list"},
		{"--upgrade", "--glob"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) = nil error, want a conflict", args)
		}
	}
}

func TestParseOptionsVersionAliases(t *testing.T) {
	for _, flag := range []string{"-v", "--version"} {
		opts, err := parseOptions([]string{flag})
		if err != nil {
			t.Fatalf("parseOptions(%q): %v", flag, err)
		}
		if !opts.version {
			t.Errorf("parseOptions(%q) version = false, want true", flag)
		}
	}
}

func TestParseOptionsSkipVersionCheckAliases(t *testing.T) {
	for _, flag := range []string{"-n", "--skip-version-check"} {
		opts, err := parseOptions([]string{"--version", flag})
		if err != nil {
			t.Fatalf("parseOptions(%q): %v", flag, err)
		}
		if !opts.skipVersionCheck {
			t.Errorf("parseOptions(%q) skipVersionCheck = false, want true", flag)
		}
	}
}

func TestParseOptionsExpandsOutputPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	opts, err := parseOptions([]string{"acme/tool/file", "--output", "~/bin/tool"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "bin", "tool"); opts.output != want {
		t.Fatalf("output = %q, want %q", opts.output, want)
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

func TestParseOptionsFiles(t *testing.T) {
	opts, err := parseOptions([]string{
		"acme/tool/bundle.zip",
		"--extract",
		"--file", "bin/tool",
		"--file=completions/tool.zsh",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bin/tool", "completions/tool.zsh"}
	if len(opts.files) != len(want) {
		t.Fatalf("files = %v, want %v", opts.files, want)
	}
	for index := range want {
		if opts.files[index] != want[index] {
			t.Fatalf("files = %v, want %v", opts.files, want)
		}
	}

	for _, args := range [][]string{
		{"acme/tool/bundle.zip", "--file", "bin/tool"},
		{"acme/tool/bundle.zip", "--extract", "--install", "--file", "bin/tool"},
		{"--upgrade", "--extract", "--file", "bin/tool"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) = nil error, want a conflict", args)
		}
	}
}
