package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gh "github.com/krisiasty/ghget/internal/github"
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
	got, err := os.ReadFile(filepath.Join(dir, "tool-linux"))
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
	var stdout strings.Builder
	if err := NewWithClient(client, &stdout, io.Discard).Run(context.Background(), []string{"acme/tool", "-t"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "v2\nv1\n" {
		t.Fatalf("output = %q", stdout.String())
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
