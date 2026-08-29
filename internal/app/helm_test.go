package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/krisiasty/ghget/internal/helm"
	"github.com/krisiasty/ghget/internal/platform"
	"github.com/krisiasty/ghget/internal/source"
)

func TestHelmLatestInstallUsesOfficialChecksum(t *testing.T) {
	archive := tarGzip(t, map[string]tarEntry{
		"linux-amd64/helm":       {mode: 0o755, content: "helm binary"},
		"linux-amd64/README.md":  {mode: 0o644, content: "documentation"},
		"linux-amd64/LICENSE":    {mode: 0o644, content: "license"},
		"linux-amd64/completion": {mode: 0o644, content: "completion"},
	})
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/helm4-latest-version":
			_, _ = io.WriteString(w, "v4.2.0\n")
		case "/helm-v4.2.0-linux-amd64.tar.gz.sha256":
			_, _ = io.WriteString(w, digest(archive)+"\n")
		case "/helm-v4.2.0-linux-amd64.tar.gz":
			_, _ = io.WriteString(w, archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	var stderr strings.Builder
	app := helmTestApp(t, server, platform.Platform{OS: "linux", Arch: "amd64"}, &stderr)
	if err := app.Run(context.Background(), []string{"helm", "--auto", "--install", "--dir", directory}); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(directory, "helm")
	content, err := os.ReadFile(installed) //nolint:gosec // The path is confined to the test's temporary directory.
	if err != nil || string(content) != "helm binary" {
		t.Fatalf("installed content = %q, err = %v", content, err)
	}
	wantRequests := []string{
		"/helm4-latest-version",
		"/helm-v4.2.0-linux-amd64.tar.gz.sha256",
		"/helm-v4.2.0-linux-amd64.tar.gz",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
	for _, message := range []string{
		"resolved helm to helm/helm",
		"selected helm-v4.2.0-linux-amd64.tar.gz",
		"downloaded helm-v4.2.0-linux-amd64.tar.gz.sha256",
		"verified helm-v4.2.0-linux-amd64.tar.gz",
		"installed " + installed,
	} {
		if !strings.Contains(stderr.String(), message) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), message)
		}
	}
}

func TestHelmExplicitVersionNormalizesLeadingV(t *testing.T) {
	archive := tarGzip(t, map[string]tarEntry{
		"darwin-arm64/helm": {mode: 0o755, content: "helm binary"},
	})
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/helm-v4.1.0-darwin-arm64.tar.gz.sha256":
			_, _ = io.WriteString(w, digest(archive)+"\n")
		case "/helm-v4.1.0-darwin-arm64.tar.gz":
			_, _ = io.WriteString(w, archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := helmTestApp(t, server, platform.Platform{OS: "darwin", Arch: "arm64"}, io.Discard)
	if err := app.Run(context.Background(), []string{"helm@4.1.0", "--auto", "--install", "--dir", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	wantRequests := []string{
		"/helm-v4.1.0-darwin-arm64.tar.gz.sha256",
		"/helm-v4.1.0-darwin-arm64.tar.gz",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
}

func TestHelmMissingChecksumPreventsArchiveDownload(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()

	directory := t.TempDir()
	app := helmTestApp(t, server, platform.Platform{OS: "linux", Arch: "amd64"}, io.Discard)
	err := app.Run(context.Background(), []string{
		"helm@v4.2.0",
		"--auto",
		"--install",
		"--dir",
		directory,
		"--checksum",
		strings.Repeat("0", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "download checksum asset") {
		t.Fatalf("Run() error = %v, want checksum download error", err)
	}
	wantRequests := []string{"/helm-v4.2.0-linux-amd64.tar.gz.sha256"}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "helm")); !os.IsNotExist(statErr) {
		t.Fatalf("installed binary exists after missing checksum: %v", statErr)
	}
}

func TestHelmUnsupportedPlatformFailsWithoutNetworkRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	defer server.Close()

	app := helmTestApp(t, server, platform.Platform{OS: "freebsd", Arch: "amd64"}, io.Discard)
	err := app.Run(context.Background(), []string{"helm@v4.2.0", "--auto", "--install", "--dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "does not publish releases for freebsd/amd64") {
		t.Fatalf("Run() error = %v, want unsupported platform", err)
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func helmTestApp(t *testing.T, server *httptest.Server, current platform.Platform, stderr io.Writer) *App {
	t.Helper()
	app := NewWithClient(&fakeClient{}, io.Discard, stderr)
	app.backends = map[string]source.Backend{
		"helm": helm.NewClientWithBaseURLs(server.Client(), server.URL, server.URL),
	}
	app.platform = current
	return app
}
