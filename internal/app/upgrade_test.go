package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gh "github.com/krisiasty/ghget/internal/github"
)

// upgradeAssetName is the release asset the running platform upgrades to.
func upgradeAssetName(version string) string {
	name := fmt.Sprintf("ghget_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// upgradeClient serves a release containing the current platform's binary.
func upgradeClient() *fakeClient {
	const content = "new binary"
	name := upgradeAssetName("0.1.3")
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	return &fakeClient{
		tag: "v0.1.3",
		assets: []gh.Asset{
			{Name: name, URL: "asset:" + name},
			{Name: "checksums.txt", URL: "asset:checksums"},
		},
		content: map[string]string{
			name:            content,
			"checksums.txt": digest + "  " + name + "\n",
		},
	}
}

func TestUpgradeReplacesResolvedSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "ghget-0.1.2")
	link := filepath.Join(directory, "ghget")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil { //nolint:gosec // The test fixture stands in for an installed binary.
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	client := upgradeClient()
	a := NewWithClient(client, io.Discard, io.Discard)
	a.executablePath = func() (string, error) { return link, nil }

	if err := a.Run(context.Background(), []string{"--upgrade"}); err != nil {
		t.Fatal(err)
	}
	if client.resolutions != 1 {
		t.Fatalf("ResolveLatest calls = %d, want 1", client.resolutions)
	}

	got, err := os.ReadFile(target) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(got) != "new binary" {
		t.Fatalf("upgraded content = %q, err = %v", got, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if want := os.FileMode(0o755); info.Mode().Perm() != want {
		t.Fatalf("upgraded mode = %04o, want %04o", info.Mode().Perm(), want)
	}
	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced by a regular file")
	}
}

func TestUpgradeRefusesHomebrewManagedInstall(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "Caskroom", "ghget", "0.1.2")
	if err := os.MkdirAll(directory, 0o755); err != nil { //nolint:gosec // The fixture mimics a Homebrew install directory.
		t.Fatal(err)
	}
	path := filepath.Join(directory, "ghget")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil { //nolint:gosec // The test fixture stands in for an installed binary.
		t.Fatal(err)
	}
	client := upgradeClient()
	a := NewWithClient(client, io.Discard, io.Discard)
	a.executablePath = func() (string, error) { return path, nil }

	err := a.Run(context.Background(), []string{"--upgrade"})
	if err == nil {
		t.Fatal("Homebrew-managed install was upgraded in place")
	}
	if !strings.Contains(err.Error(), "brew") {
		t.Fatalf("error = %v, want it to mention brew", err)
	}
	if len(client.downloads) != 0 {
		t.Fatalf("downloads = %v, want none before the guard passes", client.downloads)
	}
}

func TestUpgradeRefusesUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to any directory")
	}
	directory := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil { //nolint:gosec // The fixture mimics an install directory.
		t.Fatal(err)
	}
	path := filepath.Join(directory, "ghget")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil { //nolint:gosec // The test fixture stands in for an installed binary.
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil { //nolint:gosec // Removing write permission is the point of this test.
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o755) }) //nolint:gosec // Restored so the temporary directory can be removed.
	client := upgradeClient()
	a := NewWithClient(client, io.Discard, io.Discard)
	a.executablePath = func() (string, error) { return path, nil }

	err := a.Run(context.Background(), []string{"--upgrade"})
	if err == nil {
		t.Fatal("upgrade proceeded without write permission")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("error = %v, want it to suggest sudo", err)
	}
	if len(client.downloads) != 0 {
		t.Fatalf("downloads = %v, want none before the guard passes", client.downloads)
	}
}

func TestUpgradeRestoresBinaryWhenDownloadFails(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "ghget")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil { //nolint:gosec // The test fixture stands in for an installed binary.
		t.Fatal(err)
	}
	client := upgradeClient()
	// The release advertises the asset but serving it fails mid-upgrade.
	delete(client.content, upgradeAssetName("0.1.3"))
	a := NewWithClient(client, io.Discard, io.Discard)
	a.executablePath = func() (string, error) { return path, nil }

	if err := a.Run(context.Background(), []string{"--upgrade"}); err == nil {
		t.Fatal("failed upgrade reported success")
	}
	got, err := os.ReadFile(path) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(got) != "old binary" {
		t.Fatalf("binary after failed upgrade = %q, err = %v", got, err)
	}
}

func TestUpgradeReportsAlreadyCurrent(t *testing.T) {
	tests := []struct {
		version string
		tag     string
		want    bool
	}{
		{version: "0.1.3", tag: "v0.1.3", want: true},
		{version: "0.1.3", tag: "0.1.3", want: true},
		{version: "v0.1.3", tag: "v0.1.3", want: true},
		{version: "0.1.2", tag: "v0.1.3", want: false},
		{version: "dev", tag: "v0.1.3", want: false},
	}
	for _, test := range tests {
		if got := isCurrentRelease(test.version, test.tag); got != test.want {
			t.Errorf("isCurrentRelease(%q, %q) = %v, want %v", test.version, test.tag, got, test.want)
		}
	}
}
