package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each layout below is taken from a real release archive.
func TestExecutables(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		entries map[string]fs.FileMode
		want    []string
	}{
		{
			name: "flat single binary",
			goos: "darwin",
			// junegunn/fzf
			entries: map[string]fs.FileMode{"fzf": 0o755},
			want:    []string{"fzf"},
		},
		{
			name: "wrapper directory beside documentation",
			goos: "darwin",
			// sharkdp/bat
			entries: map[string]fs.FileMode{
				"bat-v0.26.1-x86_64-apple-darwin/bat":                  0o755,
				"bat-v0.26.1-x86_64-apple-darwin/bat.1":                0o644,
				"bat-v0.26.1-x86_64-apple-darwin/README.md":            0o644,
				"bat-v0.26.1-x86_64-apple-darwin/autocomplete/bat.zsh": 0o644,
			},
			want: []string{"bat-v0.26.1-x86_64-apple-darwin/bat"},
		},
		{
			name: "bin directory wins over the rest of the prefix",
			goos: "darwin",
			// cli/cli
			entries: map[string]fs.FileMode{
				"gh_2.98.0_macOS_amd64/bin/gh":                  0o755,
				"gh_2.98.0_macOS_amd64/LICENSE":                 0o644,
				"gh_2.98.0_macOS_amd64/share/man/man1/gh-api.1": 0o644,
				"gh_2.98.0_macOS_amd64/share/man/man1/gh-pr.1":  0o644,
			},
			want: []string{"gh_2.98.0_macOS_amd64/bin/gh"},
		},
		{
			name: "several programs in one archive",
			goos: "darwin",
			// astral-sh/uv
			entries: map[string]fs.FileMode{
				"uv-x86_64-apple-darwin/uv":  0o755,
				"uv-x86_64-apple-darwin/uvx": 0o755,
			},
			want: []string{"uv-x86_64-apple-darwin/uv", "uv-x86_64-apple-darwin/uvx"},
		},
		{
			name: "shared libraries beside a bin directory",
			goos: "linux",
			// neovim/neovim
			entries: map[string]fs.FileMode{
				"nvim-linux-x86_64/bin/nvim":                    0o755,
				"nvim-linux-x86_64/lib/nvim/parser/c.so":        0o755,
				"nvim-linux-x86_64/share/nvim/runtime/init.vim": 0o644,
			},
			want: []string{"nvim-linux-x86_64/bin/nvim"},
		},
		{
			name: "shared library at the top level is not a program",
			goos: "linux",
			entries: map[string]fs.FileMode{
				"tool":            0o755,
				"libsupport.so.1": 0o755,
			},
			want: []string{"tool"},
		},
		{
			// MS-DOS ZIPs record no Unix mode, so every file arrives as 0644.
			name: "windows archive without mode bits",
			goos: "windows",
			// BurntSushi/ripgrep
			entries: map[string]fs.FileMode{
				"ripgrep-15.2.0-x86_64-pc-windows-msvc/rg.exe":           0o644,
				"ripgrep-15.2.0-x86_64-pc-windows-msvc/README.md":        0o644,
				"ripgrep-15.2.0-x86_64-pc-windows-msvc/complete/rg.bash": 0o644,
			},
			want: []string{"ripgrep-15.2.0-x86_64-pc-windows-msvc/rg.exe"},
		},
		{
			name: "windows archive with mode bits still selects by extension",
			goos: "windows",
			// cli/cli
			entries: map[string]fs.FileMode{
				"gh_2.98.0_windows_amd64/bin/gh.exe": 0o755,
				"gh_2.98.0_windows_amd64/LICENSE":    0o644,
			},
			want: []string{"gh_2.98.0_windows_amd64/bin/gh.exe"},
		},
		{
			name: "an executable script on Windows is not a program",
			goos: "windows",
			entries: map[string]fs.FileMode{
				"tool/tool.exe":   0o755,
				"tool/install.sh": 0o755,
			},
			want: []string{"tool/tool.exe"},
		},
		{
			name: "deeper executables lose to shallower ones",
			goos: "linux",
			entries: map[string]fs.FileMode{
				"tool":          0o755,
				"extras/helper": 0o755,
			},
			want: []string{"tool"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := buildTree(t, tt.entries)
			got, err := Executables(root, tt.goos)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(relative(t, root, got), "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("Executables() = %v, want %v", relative(t, root, got), tt.want)
			}
		})
	}
}

func TestExecutablesReportsArchivesWithoutPrograms(t *testing.T) {
	root := buildTree(t, map[string]fs.FileMode{
		"docs/README.md": 0o644,
		"docs/guide.md":  0o644,
	})
	_, err := Executables(root, "linux")
	if !errors.Is(err, ErrNoExecutables) {
		t.Fatalf("Executables() error = %v, want ErrNoExecutables", err)
	}
}

func buildTree(t *testing.T, entries map[string]fs.FileMode) string {
	t.Helper()
	root := t.TempDir()
	for name, mode := range entries {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // The fixture mimics the directories a release archive unpacks to.
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func relative(t *testing.T, root string, paths []string) []string {
	t.Helper()
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		name, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.ToSlash(name))
	}
	return names
}
