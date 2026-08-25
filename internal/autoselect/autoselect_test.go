package autoselect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krisiasty/ghget/internal/platform"
)

const (
	glibc = platform.Glibc
	musl  = platform.Musl
	none  = platform.LibcNone
)

// TestSelectCorpus pins asset selection against real release listings captured
// from popular projects, which is where the naming conventions actually live.
func TestSelectCorpus(t *testing.T) {
	tests := []struct {
		fixture string
		goos    string
		goarch  string
		libc    platform.Libc
		want    string
	}{
		// Rust target triples, archives only.
		{"astral-sh_uv", "darwin", "arm64", none, "uv-aarch64-apple-darwin.tar.gz"},
		{"astral-sh_uv", "darwin", "amd64", none, "uv-x86_64-apple-darwin.tar.gz"},
		{"astral-sh_uv", "linux", "amd64", glibc, "uv-x86_64-unknown-linux-gnu.tar.gz"},
		{"astral-sh_uv", "linux", "amd64", musl, "uv-x86_64-unknown-linux-musl.tar.gz"},
		{"astral-sh_uv", "linux", "arm64", glibc, "uv-aarch64-unknown-linux-gnu.tar.gz"},
		{"astral-sh_uv", "windows", "amd64", none, "uv-x86_64-pc-windows-msvc.zip"},

		// No glibc build exists, so the musl build is the only usable one.
		{"BurntSushi_ripgrep", "linux", "amd64", glibc, "ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz"},
		{"BurntSushi_ripgrep", "linux", "arm64", glibc, "ripgrep-15.2.0-aarch64-unknown-linux-gnu.tar.gz"},
		{"BurntSushi_ripgrep", "linux", "arm64", musl, "ripgrep-15.2.0-aarch64-unknown-linux-musl.tar.gz"},
		{"BurntSushi_ripgrep", "darwin", "arm64", none, "ripgrep-15.2.0-aarch64-apple-darwin.tar.gz"},
		// MSVC is preferred over the MinGW build.
		{"BurntSushi_ripgrep", "windows", "amd64", none, "ripgrep-15.2.0-x86_64-pc-windows-msvc.zip"},

		// GoReleaser layout; .deb, .rpm, .msi, and .pkg are never candidates.
		{"cli_cli", "darwin", "arm64", none, "gh_2.98.0_macOS_arm64.zip"},
		{"cli_cli", "linux", "amd64", glibc, "gh_2.98.0_linux_amd64.tar.gz"},
		{"cli_cli", "windows", "amd64", none, "gh_2.98.0_windows_amd64.zip"},

		{"sharkdp_bat", "darwin", "arm64", none, "bat-v0.26.1-aarch64-apple-darwin.tar.gz"},
		{"sharkdp_bat", "linux", "amd64", glibc, "bat-v0.26.1-x86_64-unknown-linux-gnu.tar.gz"},
		{"sharkdp_bat", "linux", "amd64", musl, "bat-v0.26.1-x86_64-unknown-linux-musl.tar.gz"},

		{"junegunn_fzf", "linux", "amd64", glibc, "fzf-0.74.3-linux_amd64.tar.gz"},
		{"junegunn_fzf", "darwin", "arm64", none, "fzf-0.74.3-darwin_arm64.tar.gz"},
		{"junegunn_fzf", "windows", "amd64", none, "fzf-0.74.3-windows_amd64.zip"},

		// Raw binaries with no extension.
		{"jqlang_jq", "linux", "amd64", glibc, "jq-linux-amd64"},
		{"jqlang_jq", "linux", "arm64", glibc, "jq-linux-arm64"},
		{"jqlang_jq", "windows", "amd64", none, "jq-windows-amd64.exe"},

		// Mixed capitalisation, plus .sbom.json sidecars beside every archive.
		{"derailed_k9s", "darwin", "arm64", none, "k9s_Darwin_arm64.tar.gz"},
		{"derailed_k9s", "linux", "amd64", glibc, "k9s_Linux_amd64.tar.gz"},
		{"derailed_k9s", "windows", "amd64", none, "k9s_Windows_amd64.zip"},

		// AppImage, .zsync, and .msi are excluded; win64 implies a 64-bit build.
		{"neovim_neovim", "linux", "amd64", glibc, "nvim-linux-x86_64.tar.gz"},
		{"neovim_neovim", "darwin", "arm64", none, "nvim-macos-arm64.tar.gz"},
		{"neovim_neovim", "windows", "amd64", none, "nvim-win64.zip"},
		{"neovim_neovim", "windows", "arm64", none, "nvim-win-arm64.zip"},

		{"kubernetes-sigs_kind", "darwin", "arm64", none, "kind-darwin-arm64"},
		{"kubernetes-sigs_kind", "linux", "amd64", glibc, "kind-linux-amd64"},

		{"docker_compose", "linux", "amd64", glibc, "docker-compose-linux-x86_64"},
		{"docker_compose", "darwin", "arm64", none, "docker-compose-darwin-aarch64"},
		{"docker_compose", "windows", "amd64", none, "docker-compose-windows-x86_64.exe"},

		// A dot separates version from platform, and Windows ships both .zip and .tar.gz.
		{"prometheus_prometheus", "linux", "amd64", glibc, "prometheus-3.14.0.linux-amd64.tar.gz"},
		{"prometheus_prometheus", "windows", "amd64", none, "prometheus-3.14.0.windows-amd64.zip"},

		// _no_libgit carries extra unrecognised tokens, so the plain build wins.
		{"eza-community_eza", "linux", "amd64", glibc, "eza_x86_64-unknown-linux-gnu.tar.gz"},
		{"eza-community_eza", "linux", "arm64", glibc, "eza_aarch64-unknown-linux-gnu.tar.gz"},
		{"eza-community_eza", "windows", "amd64", none, "eza.exe_x86_64-pc-windows-gnu.zip"},

		// A bare binary is preferred over the archives beside it.
		{"jdx_mise", "linux", "amd64", glibc, "mise-v2026.8.13-linux-x64"},
		{"jdx_mise", "linux", "amd64", musl, "mise-v2026.8.13-linux-x64-musl"},
		{"jdx_mise", "darwin", "arm64", none, "mise-v2026.8.13-macos-arm64"},
		{"jdx_mise", "windows", "amd64", none, "mise-v2026.8.13-windows-x64.exe"},

		// -no-web variants are demoted; only musl exists for Linux.
		{"zellij-org_zellij", "linux", "amd64", glibc, "zellij-x86_64-unknown-linux-musl.tar.gz"},
		{"zellij-org_zellij", "darwin", "arm64", none, "zellij-aarch64-apple-darwin.tar.gz"},
		{"zellij-org_zellij", "windows", "amd64", none, "zellij-x86_64-pc-windows-msvc.zip"},

		// x64 and x32 aliases.
		{"gitleaks_gitleaks", "linux", "amd64", glibc, "gitleaks_8.30.1_linux_x64.tar.gz"},
		{"gitleaks_gitleaks", "linux", "386", glibc, "gitleaks_8.30.1_linux_x32.tar.gz"},
		{"gitleaks_gitleaks", "darwin", "arm64", none, "gitleaks_8.30.1_darwin_arm64.tar.gz"},

		{"starship_starship", "linux", "amd64", glibc, "starship-x86_64-unknown-linux-gnu.tar.gz"},
		// Only a musl build exists for arm64, and it is usable on a glibc host.
		{"starship_starship", "linux", "arm64", glibc, "starship-aarch64-unknown-linux-musl.tar.gz"},
		{"starship_starship", "windows", "amd64", none, "starship-x86_64-pc-windows-msvc.zip"},

		// "64bit" and mixed-case OS names.
		{"aquasecurity_trivy", "linux", "amd64", glibc, "trivy_0.74.0_Linux-64bit.tar.gz"},
		{"aquasecurity_trivy", "darwin", "arm64", none, "trivy_0.74.0_macOS-ARM64.tar.gz"},
		{"aquasecurity_trivy", "windows", "amd64", none, "trivy_0.74.0_windows-64bit.zip"},

		{"goreleaser_goreleaser", "linux", "amd64", glibc, "goreleaser_Linux_x86_64.tar.gz"},
		{"goreleaser_goreleaser", "darwin", "arm64", none, "goreleaser_Darwin_arm64.tar.gz"},
		{"goreleaser_goreleaser", "windows", "amd64", none, "goreleaser_Windows_x86_64.zip"},

		// Bare binaries buried among signature and attestation sidecars.
		{"sigstore_cosign", "linux", "amd64", glibc, "cosign-linux-amd64"},
		{"sigstore_cosign", "darwin", "arm64", none, "cosign-darwin-arm64"},
		{"sigstore_cosign", "windows", "amd64", none, "cosign-windows-amd64.exe"},

		// -rocm and -mlx accelerator builds are demoted below the plain build.
		{"ollama_ollama", "linux", "amd64", glibc, "ollama-linux-amd64.tar.zst"},
		{"ollama_ollama", "windows", "amd64", none, "ollama-windows-amd64.zip"},
	}

	for _, tt := range tests {
		name := tt.fixture + "/" + tt.goos + "_" + tt.goarch
		if tt.libc != none {
			name += "_" + tt.libc.String()
		}
		t.Run(name, func(t *testing.T) {
			target := platform.Platform{OS: tt.goos, Arch: tt.goarch, Libc: tt.libc}
			result, err := Select(loadFixture(t, tt.fixture), target)
			if err != nil {
				t.Fatalf("Select() error = %v (ranked: %v)", err, viableNames(result))
			}
			if result.Selected != tt.want {
				t.Fatalf("Select() = %q, want %q (ranked: %v)", result.Selected, tt.want, viableNames(result))
			}
		})
	}
}

// TestSelectReportsNothingForUnpublishedPlatforms guards the other half of the
// contract: silence is correct when a project ships no build for a platform.
func TestSelectReportsNothingForUnpublishedPlatforms(t *testing.T) {
	// eza publishes no macOS build at all.
	_, err := Select(loadFixture(t, "eza-community_eza"), platform.Platform{OS: "darwin", Arch: "arm64"})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("Select() error = %v, want ErrNoMatch", err)
	}
}

func TestSelectReportsAmbiguousCandidates(t *testing.T) {
	// jq publishes the same macOS binary under both a macos and an osx name.
	target := platform.Platform{OS: "darwin", Arch: "amd64", Libc: none}
	result, err := Select(loadFixture(t, "jqlang_jq"), target)
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("Select() error = %v, want *AmbiguousError (selected %q)", err, result.Selected)
	}
	want := []string{"jq-macos-amd64", "jq-osx-amd64"}
	if strings.Join(ambiguous.Names, ",") != strings.Join(want, ",") {
		t.Fatalf("ambiguous names = %v, want %v", ambiguous.Names, want)
	}
	if result.Selected != "" {
		t.Fatalf("Selected = %q, want empty when ambiguous", result.Selected)
	}
	// --first resolves the tie by taking the top-ranked candidate.
	if got := viableNames(result); len(got) < 2 || got[0] != want[0] {
		t.Fatalf("ranked candidates = %v, want %q first", got, want[0])
	}
}

func TestSelectReportsNoMatch(t *testing.T) {
	// kind publishes nothing for s390x.
	target := platform.Platform{OS: "linux", Arch: "s390x", Libc: glibc}
	result, err := Select(loadFixture(t, "kubernetes-sigs_kind"), target)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("Select() error = %v, want ErrNoMatch", err)
	}
	if len(result.Rejected) == 0 {
		t.Fatal("Rejected is empty, want every asset explained")
	}
	for _, candidate := range result.Rejected {
		if candidate.Reason == "" {
			t.Fatalf("rejected candidate %q has no reason", candidate.Name)
		}
	}
}

func TestSelectRejectsForeignPlatformsAndSidecars(t *testing.T) {
	target := platform.Platform{OS: "linux", Arch: "amd64", Libc: glibc}
	result, err := Select(loadFixture(t, "junegunn_fzf"), target)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range result.Viable {
		switch {
		case strings.Contains(candidate.Name, "android"), strings.Contains(candidate.Name, "freebsd"):
			t.Fatalf("foreign OS asset %q survived filtering", candidate.Name)
		case strings.HasSuffix(candidate.Name, ".deb"), strings.HasSuffix(candidate.Name, ".txt"):
			t.Fatalf("non-executable asset %q survived filtering", candidate.Name)
		case strings.Contains(candidate.Name, "armv"), strings.Contains(candidate.Name, "s390x"):
			t.Fatalf("foreign architecture asset %q survived filtering", candidate.Name)
		}
	}
}

func TestSelectRejectsEmptyAssetList(t *testing.T) {
	if _, err := Select(nil, platform.Platform{OS: "linux", Arch: "amd64", Libc: glibc}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("Select() error = %v, want ErrNoMatch", err)
	}
}

func loadFixture(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".txt")) //nolint:gosec // The path is built from a fixture name in this test file.
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.TrimSpace(string(data)))
}

func viableNames(result Result) []string {
	names := make([]string, 0, len(result.Viable))
	for _, candidate := range result.Viable {
		names = append(names, candidate.Name)
	}
	return names
}
