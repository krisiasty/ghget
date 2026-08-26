package kubernetes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	platformpkg "github.com/krisiasty/ghget/internal/platform"
	"github.com/krisiasty/ghget/internal/source"
)

func TestResolveLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/stable.txt" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "v1.36.2\n")
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	version, err := client.ResolveLatest(context.Background(), target("kubectl", "linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.36.2" {
		t.Fatalf("ResolveLatest() = %q, want v1.36.2", version)
	}
}

func TestResolveLatestRejectsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "../../unexpected")
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	_, err := client.ResolveLatest(context.Background(), target("kubectl", "linux", "amd64"))
	if err == nil || !strings.Contains(err.Error(), "invalid stable Kubernetes version") {
		t.Fatalf("ResolveLatest() error = %v, want invalid version", err)
	}
}

func TestListAssetsConstructsTrustedPlatformURLs(t *testing.T) {
	tests := []struct {
		name      string
		component string
		goos      string
		goarch    string
		wantName  string
		wantPath  string
	}{
		{
			name:      "Linux kubectl",
			component: "kubectl",
			goos:      "linux",
			goarch:    "arm64",
			wantName:  "kubectl-linux-arm64",
			wantPath:  "/release/v1.36.2/bin/linux/arm64/kubectl",
		},
		{
			name:      "Windows kubeadm",
			component: "kubeadm",
			goos:      "windows",
			goarch:    "amd64",
			wantName:  "kubeadm-windows-amd64.exe",
			wantPath:  "/release/v1.36.2/bin/windows/amd64/kubeadm.exe",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewClientWithBaseURL(http.DefaultClient, "https://downloads.test")
			assets, err := client.ListAssets(
				context.Background(),
				target(test.component, test.goos, test.goarch),
				"v1.36.2",
			)
			if err != nil {
				t.Fatal(err)
			}
			want := []source.Asset{
				{
					Name:             test.wantName,
					URL:              "https://downloads.test" + test.wantPath,
					ChecksumRequired: true,
				},
				{Name: test.wantName + ".sha256", URL: "https://downloads.test" + test.wantPath + ".sha256"},
			}
			if !reflect.DeepEqual(assets, want) {
				t.Fatalf("ListAssets() = %#v, want %#v", assets, want)
			}
		})
	}
}

func TestListAssetsRejectsUnsupportedInputs(t *testing.T) {
	client := NewClientWithBaseURL(http.DefaultClient, "https://downloads.test")
	tests := []struct {
		name    string
		target  source.Target
		version string
		want    string
	}{
		{
			name:    "component",
			target:  target("../kubectl", "linux", "amd64"),
			version: "v1.36.2",
			want:    "unsupported Kubernetes component",
		},
		{
			name:    "platform",
			target:  target("kubeadm", "darwin", "arm64"),
			version: "v1.36.2",
			want:    "does not publish kubeadm for darwin/arm64",
		},
		{
			name:    "version",
			target:  target("kubectl", "linux", "amd64"),
			version: "../../v1.36.2",
			want:    "invalid Kubernetes version",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.ListAssets(context.Background(), test.target, test.version)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ListAssets() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDownloadRestrictsAndFetchesConstructedAssets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/v1.36.2/bin/linux/amd64/kubectl" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "binary")
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	assets, err := client.ListAssets(context.Background(), target("kubectl", "linux", "amd64"), "v1.36.2")
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := client.Download(context.Background(), assets[0])
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(content) != "binary" {
		t.Fatalf("downloaded content = %q, want binary", content)
	}

	_, _, err = client.Download(context.Background(), source.Asset{Name: "kubectl", URL: server.URL + "/outside"})
	if err == nil || !strings.Contains(err.Error(), "refusing Kubernetes asset URL") {
		t.Fatalf("Download() error = %v, want source restriction", err)
	}
}

func TestDownloadRejectsRedirectOutsideReleaseSource(t *testing.T) {
	var outsideRequests int
	var outsideURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release/v1.36.2/bin/linux/amd64/kubectl" {
			http.Redirect(w, r, outsideURL+"/release/stolen", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		outsideRequests++
		_, _ = io.WriteString(w, "untrusted")
	}))
	defer outside.Close()
	outsideURL = outside.URL

	client := NewClientWithBaseURL(server.Client(), server.URL)
	assets, err := client.ListAssets(context.Background(), target("kubectl", "linux", "amd64"), "v1.36.2")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Download(context.Background(), assets[0])
	if err == nil || !strings.Contains(err.Error(), "outside the configured Kubernetes release source") {
		t.Fatalf("Download() error = %v, want redirect restriction", err)
	}
	if outsideRequests != 0 {
		t.Fatalf("requests to redirected host = %d, want 0", outsideRequests)
	}
}

func target(component, goos, goarch string) source.Target {
	return source.Target{
		Owner:      "kubernetes",
		Repository: component,
		Artifact:   component,
		Platform:   platformpkg.Platform{OS: goos, Arch: goarch},
	}
}
