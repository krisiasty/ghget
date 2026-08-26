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

		// denort and libdenort tie with deno on every other measure; the asset
		// carrying the project's own name is the program that was asked for.
		{"denoland_deno", "linux", "amd64", glibc, "deno-x86_64-unknown-linux-gnu.zip"},
		{"denoland_deno", "darwin", "arm64", none, "deno-aarch64-apple-darwin.zip"},
		{"denoland_deno", "windows", "amd64", none, "deno-x86_64-pc-windows-msvc.zip"},

		// A .exe extension means Windows even when the name says nothing else.
		{"zed-industries_zed", "linux", "amd64", glibc, "zed-linux-x86_64.tar.gz"},
		{"zed-industries_zed", "windows", "amd64", none, "Zed-x86_64.exe"},
		{"ytdl-org_youtube-dl", "windows", "amd64", none, "youtube-dl.exe"},
		{"alacritty_alacritty", "windows", "amd64", none, "Alacritty-v0.17.0-portable.exe"},

		// An "android" token rules an asset out on Linux, and Linux-64bit and
		// linux-amd64 name the same build, so the canonical spelling wins.
		{"oven-sh_bun", "linux", "amd64", glibc, "bun-linux-x64.zip"},
		{"oven-sh_bun", "linux", "arm64", glibc, "bun-linux-aarch64.zip"},
		{"oven-sh_bun", "darwin", "arm64", none, "bun-darwin-aarch64.zip"},
		{"oven-sh_bun", "windows", "amd64", none, "bun-windows-x64.zip"},
		{"gohugoio_hugo", "linux", "amd64", glibc, "hugo_0.165.0_linux-amd64.tar.gz"},
		{"gohugoio_hugo", "linux", "arm64", glibc, "hugo_0.165.0_linux-arm64.tar.gz"},
		{"gohugoio_hugo", "windows", "amd64", none, "hugo_0.165.0_windows-amd64.zip"},

		// protoc spells architectures with an underscore before the word size.
		{"protocolbuffers_protobuf", "linux", "amd64", glibc, "protoc-36.0-linux-x86_64.zip"},
		{"protocolbuffers_protobuf", "linux", "386", glibc, "protoc-36.0-linux-x86_32.zip"},
		{"protocolbuffers_protobuf", "linux", "arm64", glibc, "protoc-36.0-linux-aarch_64.zip"},
		{"protocolbuffers_protobuf", "linux", "s390x", glibc, "protoc-36.0-linux-s390_64.zip"},
		// A native build is preferred over the universal one.
		{"protocolbuffers_protobuf", "darwin", "arm64", none, "protoc-36.0-osx-aarch_64.zip"},
		{"protocolbuffers_protobuf", "windows", "amd64", none, "protoc-36.0-win64.zip"},

		// An Electron app: .zip is the program, .appimage/.deb/.flatpak are not,
		// and the plain .exe beats the NSIS installer beside it.
		{"toeverything_AFFiNE", "linux", "amd64", glibc, "affine-0.27.4-stable-linux-x64.zip"},
		{"toeverything_AFFiNE", "darwin", "arm64", none, "affine-0.27.4-stable-macos-arm64.zip"},
		{"toeverything_AFFiNE", "windows", "amd64", none, "affine-0.27.4-stable-windows-x64.exe"},

		{"coder_code-server", "linux", "amd64", glibc, "code-server-4.134.0-linux-amd64.tar.gz"},
		{"coder_code-server", "darwin", "arm64", none, "code-server-4.134.0-macos-arm64.tar.gz"},
		{"Genymobile_scrcpy", "linux", "amd64", glibc, "scrcpy-linux-x86_64-v4.1.tar.gz"},
		{"Genymobile_scrcpy", "windows", "amd64", none, "scrcpy-win64-v4.1.zip"},

		// A .app bundle is a macOS application whatever else the name says.
		{"cline_cline", "darwin", "arm64", none, "Cline_0.0.17_universal.app.tar.gz"},

		// The OS is glued into the product name rather than standing alone.
		{"microsoft_terminal", "windows", "amd64", none, "Microsoft.WindowsTerminal_1.24.11911.0_x64.zip"},

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
			result, err := Select(loadFixture(t, tt.fixture), target, projectOf(tt.fixture))
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
// TestSelectIgnoresUnlabelledAssetsInALabelledRelease covers the assets that
// name an architecture but no operating system. PowerToys publishes debug
// symbols as symbols-x64.zip, which must not be mistaken for a Linux build.
func TestSelectIgnoresUnlabelledAssetsInALabelledRelease(t *testing.T) {
	tests := []struct {
		fixture string
		project string
		target  platform.Platform
	}{
		{"microsoft_PowerToys", "PowerToys", platform.Platform{OS: "linux", Arch: "amd64", Libc: glibc}},
		{"microsoft_PowerToys", "PowerToys", platform.Platform{OS: "darwin", Arch: "arm64"}},
		// A macOS .app bundle carries an architecture but no operating system.
		{"unslothai_unsloth", "unsloth", platform.Platform{OS: "linux", Arch: "arm64", Libc: glibc}},
		// A universal .app bundle names no OS and passes the architecture test.
		{"cline_cline", "cline", platform.Platform{OS: "linux", Arch: "amd64", Libc: glibc}},
		// OBS publishes a .dmg and a dSYMs bundle; neither is an installable program.
		{"obsproject_obs-studio", "obs-studio", platform.Platform{OS: "darwin", Arch: "arm64"}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+"/"+tt.target.OS+"_"+tt.target.Arch, func(t *testing.T) {
			result, err := Select(loadFixture(t, tt.fixture), tt.target, tt.project)
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("Select() = %q (err %v), want ErrNoMatch", result.Selected, err)
			}
		})
	}
}

// A release that labels no operating system anywhere is taken at face value.
func TestSelectAcceptsUnlabelledAssetsWhenNothingIsLabelled(t *testing.T) {
	names := []string{"tool-amd64.tar.gz", "tool-arm64.tar.gz", "checksums.txt"}
	result, err := Select(names, platform.Platform{OS: "linux", Arch: "amd64", Libc: glibc}, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if result.Selected != "tool-amd64.tar.gz" {
		t.Fatalf("Select() = %q, want tool-amd64.tar.gz", result.Selected)
	}
}

func TestSelectRejectsAnOSNamedInsideAWord(t *testing.T) {
	// Windows Terminal spells its only platform inside "WindowsTerminal".
	for _, target := range []platform.Platform{
		{OS: "linux", Arch: "amd64", Libc: glibc},
		{OS: "darwin", Arch: "arm64"},
	} {
		result, err := Select(loadFixture(t, "microsoft_terminal"), target, "terminal")
		if !errors.Is(err, ErrNoMatch) {
			t.Fatalf("Select(%s) = %q, want ErrNoMatch", target.OS, result.Selected)
		}
	}
}

func TestSelectReportsNothingForUnpublishedPlatforms(t *testing.T) {
	// eza publishes no macOS build at all.
	_, err := Select(loadFixture(t, "eza-community_eza"), platform.Platform{OS: "darwin", Arch: "arm64"}, "eza")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("Select() error = %v, want ErrNoMatch", err)
	}
}

func TestSelectReportsAmbiguousCandidates(t *testing.T) {
	// jq publishes the same macOS binary under both a macos and an osx name.
	target := platform.Platform{OS: "darwin", Arch: "amd64", Libc: none}
	result, err := Select(loadFixture(t, "jqlang_jq"), target, "jq")
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
	result, err := Select(loadFixture(t, "kubernetes-sigs_kind"), target, "kind")
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
	result, err := Select(loadFixture(t, "junegunn_fzf"), target, "fzf")
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
	if _, err := Select(nil, platform.Platform{OS: "linux", Arch: "amd64", Libc: glibc}, "tool"); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("Select() error = %v, want ErrNoMatch", err)
	}
}

// projectOf derives the repository name from a fixture named owner_project.
func projectOf(fixture string) string {
	return fixture[strings.LastIndex(fixture, "_")+1:]
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
