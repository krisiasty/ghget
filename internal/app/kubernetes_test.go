package app

import (
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

	"github.com/krisiasty/ghget/internal/kubernetes"
	"github.com/krisiasty/ghget/internal/platform"
	"github.com/krisiasty/ghget/internal/source"
)

func TestKubernetesLatestInstallUsesOfficialChecksum(t *testing.T) {
	const content = "kubectl binary"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/release/stable.txt":
			_, _ = io.WriteString(w, "v1.36.2\n")
		case "/release/v1.36.2/bin/linux/amd64/kubectl.sha256":
			_, _ = io.WriteString(w, digest+"\n")
		case "/release/v1.36.2/bin/linux/amd64/kubectl":
			_, _ = io.WriteString(w, content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	var stderr strings.Builder
	app := kubernetesTestApp(t, server, platform.Platform{OS: "linux", Arch: "amd64"}, &stderr)
	if err := app.Run(context.Background(), []string{"kubectl", "--auto", "--install", "--dir", directory}); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(directory, "kubectl")
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
		"/release/stable.txt",
		"/release/v1.36.2/bin/linux/amd64/kubectl.sha256",
		"/release/v1.36.2/bin/linux/amd64/kubectl",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
	for _, message := range []string{
		"resolved kubectl to kubernetes/kubectl",
		"selected kubectl-linux-amd64",
		"downloaded kubectl-linux-amd64.sha256",
		"verified kubectl-linux-amd64",
		"installed " + installed,
	} {
		if !strings.Contains(stderr.String(), message) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), message)
		}
	}
}

func TestKubernetesExplicitVersionSkipsStableLookup(t *testing.T) {
	const content = "kubeadm binary"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/release/v1.35.1/bin/linux/arm64/kubeadm.sha256":
			_, _ = io.WriteString(w, digest+"\n")
		case "/release/v1.35.1/bin/linux/arm64/kubeadm":
			_, _ = io.WriteString(w, content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	app := kubernetesTestApp(t, server, platform.Platform{OS: "linux", Arch: "arm64"}, io.Discard)
	if err := app.Run(context.Background(), []string{"kubeadm@v1.35.1", "--auto", "--install", "--dir", directory}); err != nil {
		t.Fatal(err)
	}
	wantRequests := []string{
		"/release/v1.35.1/bin/linux/arm64/kubeadm.sha256",
		"/release/v1.35.1/bin/linux/arm64/kubeadm",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
}

func TestKubernetesMissingChecksumPreventsBinaryDownload(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()

	directory := t.TempDir()
	app := kubernetesTestApp(t, server, platform.Platform{OS: "linux", Arch: "amd64"}, io.Discard)
	err := app.Run(context.Background(), []string{
		"kubectl@v1.36.2",
		"--auto",
		"--install",
		"--dir",
		directory,
		"--checksum",
		strings.Repeat("0", sha256.Size*2),
	})
	if err == nil || !strings.Contains(err.Error(), "download checksum asset") {
		t.Fatalf("Run() error = %v, want checksum download error", err)
	}
	wantRequests := []string{"/release/v1.36.2/bin/linux/amd64/kubectl.sha256"}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "kubectl")); !os.IsNotExist(statErr) {
		t.Fatalf("installed binary exists after missing checksum: %v", statErr)
	}
}

func TestKubernetesChecksumMismatchPreventsInstallation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release/v1.36.2/bin/linux/amd64/kubectl.sha256":
			_, _ = io.WriteString(w, strings.Repeat("0", sha256.Size*2)+"\n")
		case "/release/v1.36.2/bin/linux/amd64/kubectl":
			_, _ = io.WriteString(w, "kubectl binary")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	app := kubernetesTestApp(t, server, platform.Platform{OS: "linux", Arch: "amd64"}, io.Discard)
	err := app.Run(context.Background(), []string{"kubectl@v1.36.2", "--auto", "--install", "--dir", directory})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Run() error = %v, want checksum mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "kubectl")); !os.IsNotExist(statErr) {
		t.Fatalf("installed binary exists after checksum mismatch: %v", statErr)
	}
}

func TestKubernetesUnsupportedPlatformFailsWithoutNetworkRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	defer server.Close()

	app := kubernetesTestApp(t, server, platform.Platform{OS: "darwin", Arch: "arm64"}, io.Discard)
	err := app.Run(context.Background(), []string{"kubeadm@v1.36.2", "--auto", "--install", "--dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "does not publish kubeadm for darwin/arm64") {
		t.Fatalf("Run() error = %v, want unsupported platform", err)
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func kubernetesTestApp(t *testing.T, server *httptest.Server, current platform.Platform, stderr io.Writer) *App {
	t.Helper()
	app := NewWithClient(&fakeClient{}, io.Discard, stderr)
	app.backends = map[string]source.Backend{
		"kubernetes": kubernetes.NewClientWithBaseURL(server.Client(), server.URL),
	}
	app.platform = current
	return app
}
