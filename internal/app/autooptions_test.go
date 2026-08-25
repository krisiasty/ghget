package app

import (
	"strings"
	"testing"
)

func TestParseOptionsAutoAndInstall(t *testing.T) {
	opts, err := parseOptions([]string{"-a", "-i", "acme/tool"})
	if err != nil {
		t.Fatalf("parseOptions(-a -i): %v", err)
	}
	if !opts.auto {
		t.Fatal("auto = false, want true")
	}
	if !opts.install {
		t.Fatal("install = false, want true")
	}
	long, err := parseOptions([]string{"--auto", "--install", "acme/tool"})
	if err != nil {
		t.Fatalf("parseOptions(--auto --install): %v", err)
	}
	if !long.auto || !long.install {
		t.Fatalf("long options = %+v, want auto and install set", long)
	}
}

func TestParseOptionsInstallIsIndependentOfAuto(t *testing.T) {
	opts, err := parseOptions([]string{"acme/tool/tool-linux-amd64.tar.gz", "--install"})
	if err != nil {
		t.Fatalf("parseOptions(--install with a pattern): %v", err)
	}
	if opts.auto {
		t.Fatal("auto = true, want --install to work without --auto")
	}
}

func TestParseOptionsRejectsConflictingModes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"install with extract", []string{"acme/tool", "--install", "--extract"}, "--install cannot be combined with --extract"},
		{"install with flat", []string{"acme/tool", "--install", "--flat"}, "--flat requires --extract"},
		{"auto with list", []string{"acme/tool", "--auto", "--list"}, "--auto cannot be combined with --list"},
		{"auto with tag listing", []string{"acme/tool", "--auto", "--tag"}, "--auto cannot be combined with --tag"},
		{"auto with glob", []string{"acme/tool", "--auto", "--glob"}, "--auto cannot be combined with --glob or --regex"},
		{"auto with regex", []string{"acme/tool", "--auto", "--regex"}, "--auto cannot be combined with --glob or --regex"},
		{"first without auto", []string{"acme/tool/file", "--first"}, "--first requires --auto"},
		{"upgrade with auto", []string{"--upgrade", "--auto"}, "--upgrade cannot be combined with --auto"},
		{"upgrade with install", []string{"--upgrade", "--install"}, "--upgrade cannot be combined with --install"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseOptions(tt.args)
			if err == nil {
				t.Fatalf("parseOptions(%v) succeeded, want %q", tt.args, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseOptions(%v) error = %q, want it to contain %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestParseOptionsAllowsKeepWithInstall(t *testing.T) {
	if _, err := parseOptions([]string{"acme/tool", "--auto", "--install", "--keep"}); err != nil {
		t.Fatalf("parseOptions(--install --keep): %v", err)
	}
	if _, err := parseOptions([]string{"acme/tool", "--keep"}); err == nil {
		t.Fatal("--keep alone succeeded, want it to require --extract or --install")
	}
}
