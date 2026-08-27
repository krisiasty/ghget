package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/krisiasty/ghget/internal/hashicorp"
	"github.com/krisiasty/ghget/internal/platform"
	"github.com/krisiasty/ghget/internal/source"
)

func TestHashiCorpLatestInstallUsesOfficialChecksum(t *testing.T) {
	const (
		archiveName = "terraform_1.10.2_linux_amd64.zip"
		content     = "terraform binary"
	)
	archive := hashicorpArchive(t, "terraform", content)
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/terraform/":
			_, _ = io.WriteString(w, strings.Join([]string{
				`<a href="/terraform/1.11.0-beta1/">beta</a>`,
				`<a href="/terraform/1.10.2/">stable</a>`,
			}, "\n"))
		case "/terraform/1.10.2/":
			_, _ = io.WriteString(w, strings.Join([]string{
				`<a href="terraform_1.10.2_SHA256SUMS">checksums</a>`,
				`<a href="terraform_1.10.2_linux_amd64.zip">archive</a>`,
			}, "\n"))
		case "/terraform/1.10.2/terraform_1.10.2_SHA256SUMS":
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, archiveName)
		case "/terraform/1.10.2/" + archiveName:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	var stderr strings.Builder
	app := hashicorpTestApp(t, server, platform.Platform{OS: "linux", Arch: "amd64"}, &stderr)
	if err := app.Run(context.Background(), []string{"terraform", "--auto", "--install", "--dir", directory}); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(directory, "terraform")
	got, err := os.ReadFile(installed) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(got) != content {
		t.Fatalf("installed content = %q, err = %v", got, err)
	}
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %04o, want 0755", info.Mode().Perm())
	}
	wantRequests := []string{
		"/terraform/",
		"/terraform/1.10.2/",
		"/terraform/1.10.2/terraform_1.10.2_SHA256SUMS",
		"/terraform/1.10.2/" + archiveName,
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
	for _, message := range []string{
		"resolved terraform to hashicorp/terraform",
		"selected " + archiveName,
		"downloaded terraform_1.10.2_SHA256SUMS",
		"verified " + archiveName,
		"installed " + installed,
	} {
		if !strings.Contains(stderr.String(), message) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), message)
		}
	}
}

func TestHashiCorpExplicitVersionNormalizesLeadingV(t *testing.T) {
	const archiveName = "vault_1.19.3_darwin_arm64.zip"
	archive := hashicorpArchive(t, "vault", "vault binary")
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/vault/1.19.3/":
			_, _ = io.WriteString(w, strings.Join([]string{
				`<a href="vault_1.19.3_darwin_arm64.zip">archive</a>`,
				`<a href="vault_1.19.3_SHA256SUMS">checksums</a>`,
			}, "\n"))
		case "/vault/1.19.3/vault_1.19.3_SHA256SUMS":
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, archiveName)
		case "/vault/1.19.3/" + archiveName:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	app := hashicorpTestApp(t, server, platform.Platform{OS: "darwin", Arch: "arm64"}, io.Discard)
	if err := app.Run(context.Background(), []string{"vault@v1.19.3", "--auto", "--install", "--dir", directory}); err != nil {
		t.Fatal(err)
	}
	wantRequests := []string{
		"/vault/1.19.3/",
		"/vault/1.19.3/vault_1.19.3_SHA256SUMS",
		"/vault/1.19.3/" + archiveName,
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
}

func TestHashiCorpMissingChecksumPreventsArchiveDownload(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/terraform/1.10.2/":
			_, _ = io.WriteString(w, strings.Join([]string{
				`<a href="terraform_1.10.2_linux_amd64.zip">archive</a>`,
				`<a href="terraform_1.10.2_SHA256SUMS">checksums</a>`,
			}, "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := hashicorpTestApp(t, server, platform.Platform{OS: "linux", Arch: "amd64"}, io.Discard)
	err := app.Run(context.Background(), []string{
		"terraform@1.10.2",
		"--auto",
		"--install",
		"--dir",
		t.TempDir(),
		"--checksum",
		strings.Repeat("0", sha256.Size*2),
	})
	if err == nil || !strings.Contains(err.Error(), "download checksum asset") {
		t.Fatalf("Run() error = %v, want checksum download error", err)
	}
	wantRequests := []string{
		"/terraform/1.10.2/",
		"/terraform/1.10.2/terraform_1.10.2_SHA256SUMS",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
}

func TestHashiCorpUnavailablePlatformFailsBeforeDownloads(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.URL.Path != "/vault/1.19.3/" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, strings.Join([]string{
			`<a href="vault_1.19.3_linux_amd64.zip">archive</a>`,
			`<a href="vault_1.19.3_SHA256SUMS">checksums</a>`,
		}, "\n"))
	}))
	defer server.Close()

	app := hashicorpTestApp(t, server, platform.Platform{OS: "plan9", Arch: "amd64"}, io.Discard)
	err := app.Run(context.Background(), []string{"vault@1.19.3", "--auto", "--install", "--dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "does not publish vault 1.19.3 for plan9/amd64") {
		t.Fatalf("Run() error = %v, want unavailable platform", err)
	}
	if !reflect.DeepEqual(requests, []string{"/vault/1.19.3/"}) {
		t.Fatalf("requests = %v, want release page only", requests)
	}
}

func hashicorpArchive(t *testing.T, name, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0o755)
	file, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func hashicorpTestApp(t *testing.T, server *httptest.Server, current platform.Platform, stderr io.Writer) *App {
	t.Helper()
	app := NewWithClient(&fakeClient{}, io.Discard, stderr)
	app.backends = map[string]source.Backend{
		"hashicorp": hashicorp.NewClientWithBaseURL(server.Client(), server.URL),
	}
	app.platform = current
	return app
}
