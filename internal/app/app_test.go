package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	gh "github.com/krisiasty/ghget/internal/github"
	"github.com/krisiasty/ghget/internal/matcher"
)

type fakeClient struct {
	tag       string
	assets    []gh.Asset
	tags      []string
	content   map[string]string
	downloads []string
}

func (f *fakeClient) ResolveLatest(context.Context, string, string) (string, error) {
	return f.tag, nil
}

func (f *fakeClient) ListAssets(context.Context, string, string, string) ([]gh.Asset, error) {
	return f.assets, nil
}

func (f *fakeClient) ListTags(context.Context, string, string) ([]string, error) {
	return f.tags, nil
}

func (f *fakeClient) Download(_ context.Context, asset gh.Asset) (io.ReadCloser, int64, error) {
	f.downloads = append(f.downloads, asset.Name)
	content, ok := f.content[asset.Name]
	if !ok {
		return nil, 0, fmt.Errorf("missing fake content for %s", asset.Name)
	}
	return io.NopCloser(strings.NewReader(content)), int64(len(content)), nil
}

func TestVersion(t *testing.T) {
	var stdout strings.Builder
	if err := NewWithClient(nil, &stdout, io.Discard).Run(context.Background(), []string{"--version"}); err != nil {
		t.Fatal(err)
	}
	if want := "ghget dev (commit unknown, built unknown)\n"; stdout.String() != want {
		t.Fatalf("output = %q, want %q", stdout.String(), want)
	}
}

func TestDownloadWithAutomaticChecksum(t *testing.T) {
	content := "binary data"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	client := &fakeClient{
		tag: "v1",
		assets: []gh.Asset{
			{Name: "tool-linux", URL: "asset:tool-linux"},
			{Name: "checksums.txt", URL: "asset:checksums"},
		},
		content: map[string]string{
			"tool-linux":    content,
			"checksums.txt": digest + "  tool-linux\n",
		},
	}
	var stdout, stderr strings.Builder
	a := NewWithClient(client, &stdout, &stderr)
	dir := t.TempDir()
	if err := a.Run(context.Background(), []string{"acme/tool/tool-*", "--glob", "--dir", dir, "--executable"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "tool-linux")) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(got) != content {
		t.Fatalf("downloaded content = %q, err = %v", got, err)
	}
	info, err := os.Stat(filepath.Join(dir, "tool-linux"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("downloaded mode = %v, err = %v", info.Mode(), err)
	}
	if !reflect.DeepEqual(client.downloads, []string{"checksums.txt", "tool-linux"}) {
		t.Fatalf("downloads = %v", client.downloads)
	}
	if !strings.Contains(stderr.String(), "verified tool-linux") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	for _, path := range []string{"checksums.txt", filepath.Join(dir, "tool-linux")} {
		if !strings.Contains(stderr.String(), "downloaded "+path) {
			t.Fatalf("stderr = %q, want downloaded message for %q", stderr.String(), path)
		}
	}
}

func TestDownloadWithTagPlaceholder(t *testing.T) {
	assetName := "tlsx_1.3.0_macOS_amd64.zip"
	client := &fakeClient{
		tag:     "v1.3.0",
		assets:  []gh.Asset{{Name: assetName}},
		content: map[string]string{assetName: "archive"},
	}
	dir := t.TempDir()
	if err := NewWithClient(client, io.Discard, io.Discard).Run(context.Background(), []string{
		"projectdiscovery/tlsx/tlsx_{tag}_macOS_amd64.zip", "--dir", dir,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, assetName)) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(got) != "archive" {
		t.Fatalf("downloaded content = %q, err = %v", got, err)
	}
	if !reflect.DeepEqual(client.downloads, []string{assetName}) {
		t.Fatalf("downloads = %v", client.downloads)
	}
}

func TestDownloadWithRuntimePlaceholders(t *testing.T) {
	assetName := fmt.Sprintf("tool-%s-%s-%s.zip",
		architectureVariants(runtime.GOARCH)[0], vendorVariants(runtime.GOOS)[0], operatingSystemVariants(runtime.GOOS)[0])
	client := &fakeClient{
		tag:     "v1.0.0",
		assets:  []gh.Asset{{Name: assetName}},
		content: map[string]string{assetName: "archive"},
	}
	dir := t.TempDir()
	if err := NewWithClient(client, io.Discard, io.Discard).Run(context.Background(), []string{
		"acme/tool/{repo}-{arch}-{vendor}-{os}.zip", "--dir", dir,
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.downloads, []string{assetName}) {
		t.Fatalf("downloads = %v", client.downloads)
	}
}

func TestSelectAssetsForTag(t *testing.T) {
	tests := []struct {
		name    string
		names   []string
		pattern string
		tag     string
		mode    matcher.Mode
		want    []string
	}{
		{
			name:    "exact drops leading v",
			names:   []string{"tool_1.3.0.zip"},
			pattern: "tool_{tag}.zip",
			tag:     "v1.3.0",
			want:    []string{"tool_1.3.0.zip"},
		},
		{
			name:    "exact adds leading v",
			names:   []string{"tool_v1.3.0.zip"},
			pattern: "tool_{tag}.zip",
			tag:     "1.3.0",
			want:    []string{"tool_v1.3.0.zip"},
		},
		{
			name:    "resolved spelling takes precedence",
			names:   []string{"tool_1.3.0.zip", "tool_v1.3.0.zip"},
			pattern: "tool_{tag}.zip",
			tag:     "v1.3.0",
			want:    []string{"tool_v1.3.0.zip"},
		},
		{
			name:    "glob",
			names:   []string{"tool_1.3.0_linux_amd64.zip", "tool_1.3.0_linux_arm64.zip"},
			pattern: "tool_{tag}_linux_*.zip",
			tag:     "v1.3.0",
			mode:    matcher.Glob,
			want:    []string{"tool_1.3.0_linux_amd64.zip", "tool_1.3.0_linux_arm64.zip"},
		},
		{
			name:    "regex quotes tag",
			names:   []string{"tool_1.3.0+build_linux.zip", "tool_1x3x00build_linux.zip"},
			pattern: `^tool_{tag}_linux\.zip$`,
			tag:     "v1.3.0+build",
			mode:    matcher.Regex,
			want:    []string{"tool_1.3.0+build_linux.zip"},
		},
		{
			name:    "glob quotes non-semver tag",
			names:   []string{"tool_release[1]_linux.zip"},
			pattern: "tool_{tag}_*.zip",
			tag:     "release[1]",
			mode:    matcher.Glob,
			want:    []string{"tool_release[1]_linux.zip"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectAssets(test.names, test.pattern, placeholderValues{tag: test.tag}, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selectAssetsForTag() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSelectAssetsWithPlatformPlaceholders(t *testing.T) {
	tests := []struct {
		name    string
		names   []string
		pattern string
		values  placeholderValues
		mode    matcher.Mode
		want    []string
	}{
		{
			name:    "darwin amd64 aliases and vendor",
			names:   []string{"uv-x86_64-apple-darwin.tar.gz", "uv-amd64-apple-macos.tar.gz", "uv-aarch64-apple-darwin.tar.gz"},
			pattern: "uv-{arch}-{vendor}-{os}.tar.gz",
			values:  placeholderValues{goos: "darwin", goarch: "amd64"},
			want:    []string{"uv-x86_64-apple-darwin.tar.gz", "uv-amd64-apple-macos.tar.gz"},
		},
		{
			name:    "darwin os aliases",
			names:   []string{"tool-arm64-mac.zip", "tool-aarch64-osx.zip", "tool-arm64-linux.zip"},
			pattern: "tool-{arch}-{os}.zip",
			values:  placeholderValues{goos: "darwin", goarch: "arm64"},
			want:    []string{"tool-arm64-mac.zip", "tool-aarch64-osx.zip"},
		},
		{
			name:    "windows aliases and vendor",
			names:   []string{"tool_x86_64_pc_win.zip", "tool_amd64_pc_windows.zip", "tool_amd64_unknown_linux.zip"},
			pattern: "tool_{arch}_{vendor}_{os}.zip",
			values:  placeholderValues{goos: "windows", goarch: "amd64"},
			want:    []string{"tool_x86_64_pc_win.zip", "tool_amd64_pc_windows.zip"},
		},
		{
			name:    "linux vendor",
			names:   []string{"tool-aarch64-unknown-linux.tar.gz", "tool-arm64-pc-linux.tar.gz"},
			pattern: "tool-{arch}-{vendor}-{os}.tar.gz",
			values:  placeholderValues{goos: "linux", goarch: "arm64"},
			want:    []string{"tool-aarch64-unknown-linux.tar.gz"},
		},
		{
			name:    "repository name",
			names:   []string{"my.tool-linux.zip", "myXtool-linux.zip"},
			pattern: `^{repo}-{os}\.zip$`,
			values:  placeholderValues{repo: "my.tool", goos: "linux", goarch: "amd64"},
			mode:    matcher.Regex,
			want:    []string{"my.tool-linux.zip"},
		},
		{
			name:    "regex pattern edges",
			names:   []string{"linux-amd64", "darwin-amd64"},
			pattern: `^{os}-{arch}$`,
			values:  placeholderValues{goos: "linux", goarch: "amd64"},
			mode:    matcher.Regex,
			want:    []string{"linux-amd64"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectAssets(test.names, test.pattern, test.values, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selectAssets() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPlatformPlaceholdersRequireDelimiters(t *testing.T) {
	for _, pattern := range []string{"tool{arch}-{os}.zip", "tool-{arch}suffix-{os}.zip", "tool-{arch}-on{os}.zip"} {
		_, err := selectAssets(nil, pattern, placeholderValues{goos: "linux", goarch: "amd64"}, matcher.Exact)
		if err == nil {
			t.Errorf("selectAssets(%q) succeeded, want delimiter error", pattern)
		}
	}
}

func TestAutomaticChecksumsFetchOnlyRelevantSidecars(t *testing.T) {
	archiveName := "uv-x86_64-unknown-linux-gnu.tar.gz"
	sidecarName := archiveName + ".sha256"
	content := "binary data"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	sidecar := digest + "  " + archiveName + "\n"
	sidecarDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(sidecar)))
	client := &fakeClient{
		tag: "v1",
		assets: []gh.Asset{
			{Name: archiveName},
			{Name: sidecarName, Digest: sidecarDigest},
			{Name: "uv-aarch64-apple-darwin.tar.gz.sha256"},
			{Name: "source.tar.gz.sha256"},
		},
		content: map[string]string{
			archiveName:                             content,
			sidecarName:                             sidecar,
			"uv-aarch64-apple-darwin.tar.gz.sha256": digest,
			"source.tar.gz.sha256":                  digest,
		},
	}
	var stderr strings.Builder
	dir := t.TempDir()
	err := NewWithClient(client, io.Discard, &stderr).Run(context.Background(), []string{
		"astral-sh/uv/uv*x86*linux*", "--glob", "--dir", dir, "--debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.downloads, []string{sidecarName, archiveName}) {
		t.Fatalf("downloads = %v", client.downloads)
	}
	for name, want := range map[string]string{archiveName: content, sidecarName: sidecar} {
		got, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // The path is confined to the test's temporary directory.
		if err != nil || string(got) != want {
			t.Errorf("%s content = %q, err = %v", name, got, err)
		}
	}
	if !strings.Contains(stderr.String(), "verified "+archiveName) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "verified "+sidecarName) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "source_count=1") || !strings.Contains(stderr.String(), "sources=["+sidecarName+"]") {
		t.Fatalf("debug output = %q", stderr.String())
	}
}

func TestGitHubGeneratedChecksumFallback(t *testing.T) {
	content := "release asset without checksum file"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "tool.tar.gz", Digest: digest}},
		content: map[string]string{"tool.tar.gz": content},
	}
	var stderr strings.Builder
	dir := t.TempDir()
	err := NewWithClient(client, io.Discard, &stderr).Run(context.Background(), []string{
		"acme/tool/tool.tar.gz", "--dir", dir, "--debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "using GitHub-generated checksum fallback") ||
		!strings.Contains(stderr.String(), "verified tool.tar.gz") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExistingMatchingDownloadIsSkipped(t *testing.T) {
	content := "already downloaded"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "tool", Digest: digest}},
		content: map[string]string{"tool": content},
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if err := NewWithClient(client, io.Discard, &stderr).Run(context.Background(), []string{"acme/tool/tool", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if len(client.downloads) != 0 {
		t.Fatalf("downloads = %v, want none", client.downloads)
	}
	if !strings.Contains(stderr.String(), "already exists and checksum matches") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExistingMismatchStopsAfterChecksumDownload(t *testing.T) {
	content := "remote content"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	client := &fakeClient{
		tag: "v1",
		assets: []gh.Asset{
			{Name: "tool"},
			{Name: "tool.sha256"},
		},
		content: map[string]string{
			"tool":        content,
			"tool.sha256": digest + "  tool\n",
		},
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("different local content"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	err := NewWithClient(client, io.Discard, &stderr).Run(context.Background(), []string{"acme/tool/tool", "--dir", dir})
	if err == nil || !strings.Contains(err.Error(), "does not match the remote checksum") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(client.downloads, []string{"tool.sha256"}) {
		t.Fatalf("downloads = %v, want only checksum sidecar", client.downloads)
	}
	if got := stderr.String(); !strings.Contains(got, "downloaded tool.sha256") || strings.Contains(got, "downloaded "+filepath.Join(dir, "tool")) {
		t.Fatalf("stderr = %q", got)
	}
}

func TestForceOverwritesExistingDownload(t *testing.T) {
	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "tool"}},
		content: map[string]string{"tool": "new"},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewWithClient(client, io.Discard, io.Discard).Run(context.Background(), []string{"acme/tool/tool", "--dir", dir}); err == nil {
		t.Fatal("existing file was overwritten without --force")
	}
	if len(client.downloads) != 0 {
		t.Fatalf("downloads before force = %v, want none", client.downloads)
	}
	if err := NewWithClient(client, io.Discard, io.Discard).Run(context.Background(), []string{"acme/tool/tool", "--dir", dir, "-f"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // path is inside the test's temporary directory.
	if err != nil || string(got) != "new" {
		t.Fatalf("overwritten content = %q, err = %v", got, err)
	}
	if !reflect.DeepEqual(client.downloads, []string{"tool"}) {
		t.Fatalf("downloads after force = %v", client.downloads)
	}
}

func TestExtractKeepsArchive(t *testing.T) {
	var archiveContent bytes.Buffer
	zw := zip.NewWriter(&archiveContent)
	w, err := zw.Create("tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("executable")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "bundle.zip"}},
		content: map[string]string{"bundle.zip": archiveContent.String()},
	}
	dir := t.TempDir()
	var stderr strings.Builder
	err = NewWithClient(client, io.Discard, &stderr).Run(context.Background(), []string{
		"acme/tool/bundle.zip", "--extract", "--keep", "--dir", dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "tool")); err != nil || string(got) != "executable" { //nolint:gosec // The path is confined to the test's temporary directory.
		t.Fatalf("extracted content = %q, err = %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "bundle.zip")); err != nil || !bytes.Equal(got, archiveContent.Bytes()) { //nolint:gosec // The path is confined to the test's temporary directory.
		t.Fatalf("kept archive differs, err = %v", err)
	}
	if !strings.Contains(stderr.String(), "kept archive") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "extracted "+filepath.Join(dir, "tool")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "downloaded "+filepath.Join(dir, "bundle.zip")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExtractKeepsMatchingExistingFile(t *testing.T) {
	var archiveContent bytes.Buffer
	zw := zip.NewWriter(&archiveContent)
	w, err := zw.Create("bin/tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("same content")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "bundle.zip"}},
		content: map[string]string{"bundle.zip": archiveContent.String()},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("same content"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	err = NewWithClient(client, io.Discard, &stderr).Run(context.Background(), []string{
		"acme/tool/bundle.zip", "--extract", "--dir", dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "already exists and content matches " + path
	if got := stderr.String(); !strings.Contains(got, want) || strings.Contains(got, "extracted "+path) {
		t.Fatalf("stderr = %q", got)
	}
}

func TestExtractFlat(t *testing.T) {
	var archiveContent bytes.Buffer
	zw := zip.NewWriter(&archiveContent)
	w, err := zw.Create("nested/bin/tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{
		tag:     "v1",
		assets:  []gh.Asset{{Name: "bundle.zip"}},
		content: map[string]string{"bundle.zip": archiveContent.String()},
	}
	dir := t.TempDir()
	if err := NewWithClient(client, io.Discard, io.Discard).Run(context.Background(), []string{
		"acme/tool/bundle.zip", "--extract", "--flat", "--dir", dir,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "tool")) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(got) != "binary" {
		t.Fatalf("flat content = %q, err = %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nested")); !os.IsNotExist(err) {
		t.Fatalf("nested directory exists after flat extraction: %v", err)
	}
}

func TestExtractedDisplayPath(t *testing.T) {
	root := filepath.Join("relative", "destination")
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(absRoot, "bin", "tool")
	want := filepath.Join(root, "bin", "tool")
	if got := extractedDisplayPath(path, root); got != want {
		t.Fatalf("extractedDisplayPath() = %q, want %q", got, want)
	}
}

func TestKeepRequiresExtract(t *testing.T) {
	if _, err := parseOptions([]string{"acme/tool/file", "--keep"}); err == nil || !strings.Contains(err.Error(), "--keep requires --extract") {
		t.Fatalf("parseOptions error = %v", err)
	}
}

func TestFlatRequiresExtract(t *testing.T) {
	if _, err := parseOptions([]string{"acme/tool/file", "--flat"}); err == nil || !strings.Contains(err.Error(), "--flat requires --extract") {
		t.Fatalf("parseOptions error = %v", err)
	}
}

func TestListLatestAssets(t *testing.T) {
	client := &fakeClient{tag: "v2", assets: []gh.Asset{{Name: "a.zip"}, {Name: "b.tgz"}}}
	var stdout strings.Builder
	a := NewWithClient(client, &stdout, io.Discard)
	if err := a.Run(context.Background(), []string{"acme/tool", "--list"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "a.zip\nb.tgz\n" {
		t.Fatalf("output = %q", stdout.String())
	}
}

func TestListTags(t *testing.T) {
	client := &fakeClient{tags: []string{"v2", "v1"}}
	var stdout, stderr strings.Builder
	if err := NewWithClient(client, &stdout, &stderr).Run(context.Background(), []string{"acme/tool", "-t", "--debug"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "latest\nv1\nv2\n" {
		t.Fatalf("output = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `level=DEBUG msg="release tags listed"`) || !strings.Contains(stderr.String(), "tag_count=2") {
		t.Fatalf("debug output = %q", stderr.String())
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		input                string
		owner, repo, fileTag string
	}{
		{"owner/repo/file@v1", "owner", "repo", "file|v1"},
		{"owner/repo/file", "owner", "repo", "file|latest"},
		{"owner/repo@v2", "owner", "repo", "|v2"},
	}
	for _, tt := range tests {
		owner, repo, file, tag, err := parseTarget(tt.input)
		if err != nil {
			t.Fatal(err)
		}
		if owner != tt.owner || repo != tt.repo || file+"|"+tag != tt.fileTag {
			t.Fatalf("parseTarget(%q) = %q, %q, %q, %q", tt.input, owner, repo, file, tag)
		}
	}
}
