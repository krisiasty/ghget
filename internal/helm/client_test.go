package helm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	platformpkg "github.com/krisiasty/ghget/internal/platform"
	"github.com/krisiasty/ghget/internal/source"
)

func TestResolveLatestUsesOfficialStableMarker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/helm4-latest-version" || !decimal(r.URL.Query().Get("ts"), 20) {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control = %q, want no-cache", got)
		}
		_, _ = io.WriteString(w, "4.2.0\n")
	}))
	defer server.Close()

	client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
	version, err := client.ResolveLatest(context.Background(), target("linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if version != "v4.2.0" {
		t.Fatalf("ResolveLatest() = %q, want v4.2.0", version)
	}
}

func TestResolveLatestRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{name: "prerelease", response: "v4.3.0-rc.1\n", want: "invalid latest stable Helm version"},
		{name: "malformed", response: "../../v4.2.0\n", want: "invalid latest stable Helm version"},
		{name: "oversized", response: strings.Repeat("1", maxLatestSize+1), want: "response is too large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()

			client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
			_, err := client.ResolveLatest(context.Background(), target("linux", "amd64"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveLatest() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestListTagsUsesBoundedStableGitHubMetadata(t *testing.T) {
	var requestedPages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/helm/helm/releases" || r.URL.Query().Get("per_page") != "100" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want GitHub JSON media type", got)
		}
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))
		var releases []githubRelease
		switch r.URL.Query().Get("page") {
		case "1":
			releases = []githubRelease{
				{TagName: "v4.2.0"},
				{TagName: "v4.3.0-rc.1"},
				{TagName: "v3.20.0", Draft: true},
				{TagName: "v3.19.1", Prerelease: true},
			}
			for len(releases) < releasesPerPage {
				releases = append(releases, githubRelease{TagName: "v4.2.0"})
			}
		case "2":
			releases = []githubRelease{{TagName: "v3.19.0"}, {TagName: "3.18.0"}, {TagName: "v1.2.1"}}
		default:
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(releases); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
	tags, err := client.ListTags(context.Background(), target("linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"v4.2.0", "v3.19.0", "v3.18.0"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("ListTags() = %v, want %v", tags, want)
	}
	if wantPages := []string{"1", "2"}; !reflect.DeepEqual(requestedPages, wantPages) {
		t.Fatalf("requested pages = %v, want %v", requestedPages, wantPages)
	}
}

func TestListTagsRejectsMalformedAndOversizedMetadata(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{name: "malformed", response: "not-json", want: "decode Helm GitHub release metadata"},
		{name: "oversized", response: strings.Repeat(" ", maxMetadataSize+1), want: "response is too large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()

			client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
			_, err := client.ListTags(context.Background(), target("linux", "amd64"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ListTags() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestListAssetsConstructsTrustedPlatformURLs(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		goarch   string
		version  string
		wantName string
	}{
		{
			name:     "Linux",
			goos:     "linux",
			goarch:   "riscv64",
			version:  "4.2.0",
			wantName: "helm-v4.2.0-linux-riscv64.tar.gz",
		},
		{
			name:     "Windows",
			goos:     "windows",
			goarch:   "arm64",
			version:  "v4.2.0",
			wantName: "helm-v4.2.0-windows-arm64.tar.gz",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewClientWithBaseURLs(http.DefaultClient, "https://downloads.test", "https://metadata.test")
			assets, err := client.ListAssets(context.Background(), target(test.goos, test.goarch), test.version)
			if err != nil {
				t.Fatal(err)
			}
			want := []source.Asset{
				{
					Name:             test.wantName,
					URL:              "https://downloads.test/" + test.wantName,
					ChecksumRequired: true,
				},
				{Name: test.wantName + ".sha256", URL: "https://downloads.test/" + test.wantName + ".sha256"},
			}
			if !reflect.DeepEqual(assets, want) {
				t.Fatalf("ListAssets() = %#v, want %#v", assets, want)
			}
		})
	}
}

func TestListAssetsRejectsUnsupportedInputs(t *testing.T) {
	client := NewClientWithBaseURLs(http.DefaultClient, "https://downloads.test", "https://metadata.test")
	tests := []struct {
		name    string
		target  source.Target
		version string
		want    string
	}{
		{name: "target", target: source.Target{Owner: "other", Repository: "helm", Artifact: "helm"}, version: "v4.2.0", want: "invalid Helm source target"},
		{name: "platform", target: target("freebsd", "amd64"), version: "v4.2.0", want: "does not publish releases for freebsd/amd64"},
		{name: "prerelease", target: target("linux", "amd64"), version: "v4.3.0-rc.1", want: "invalid stable Helm version"},
		{name: "unavailable major", target: target("linux", "amd64"), version: "v1.2.1", want: "unsupported Helm major version"},
		{name: "malformed", target: target("linux", "amd64"), version: "../../v4.2.0", want: "invalid stable Helm version"},
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
		switch r.URL.Path {
		case "/helm-v4.2.0-linux-amd64.tar.gz":
			_, _ = io.WriteString(w, "archive")
		case "/helm-v4.2.0-linux-amd64.tar.gz.sha256":
			_, _ = io.WriteString(w, strings.Repeat("a", 64)+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
	assets, err := client.ListAssets(context.Background(), target("linux", "amd64"), "v4.2.0")
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"archive", strings.Repeat("a", 64) + "\n"} {
		body, _, err := client.Download(context.Background(), assets[index])
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
		if string(content) != want {
			t.Fatalf("downloaded content = %q, want %q", content, want)
		}
	}

	_, _, err = client.Download(context.Background(), source.Asset{Name: "helm.tar.gz", URL: server.URL + "/outside"})
	if err == nil || !strings.Contains(err.Error(), "refusing Helm asset URL") {
		t.Fatalf("Download() error = %v, want source restriction", err)
	}
}

func TestDownloadRejectsOversizedChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", maxChecksumSize+1))
	}))
	defer server.Close()

	client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
	assets, err := client.ListAssets(context.Background(), target("linux", "amd64"), "v4.2.0")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Download(context.Background(), assets[1])
	if err == nil || !strings.Contains(err.Error(), "response is too large") {
		t.Fatalf("Download() error = %v, want bounded checksum error", err)
	}
}

func TestDownloadRejectsRedirectOutsideReleaseSource(t *testing.T) {
	var outsideRequests int
	var outsideURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/helm-v4.2.0-linux-amd64.tar.gz" {
			http.Redirect(w, r, outsideURL+"/helm-v4.2.0-linux-amd64.tar.gz", http.StatusFound)
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

	client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
	assets, err := client.ListAssets(context.Background(), target("linux", "amd64"), "v4.2.0")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Download(context.Background(), assets[0])
	if err == nil || !strings.Contains(err.Error(), "outside the configured Helm release source") {
		t.Fatalf("Download() error = %v, want redirect restriction", err)
	}
	if outsideRequests != 0 {
		t.Fatalf("requests to redirected host = %d, want 0", outsideRequests)
	}
}

func TestDownloadRejectsRedirectToDifferentRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/helm-v4.2.0-linux-amd64.tar.gz" {
			http.Redirect(w, r, "/helm-v4.1.0-linux-amd64.tar.gz", http.StatusFound)
			return
		}
		t.Fatalf("unexpected redirected request to %s", r.URL.Path)
	}))
	defer server.Close()

	client := NewClientWithBaseURLs(server.Client(), server.URL, server.URL)
	assets, err := client.ListAssets(context.Background(), target("linux", "amd64"), "v4.2.0")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Download(context.Background(), assets[0])
	if err == nil || !strings.Contains(err.Error(), "outside the configured Helm release source") {
		t.Fatalf("Download() error = %v, want changed-resource redirect restriction", err)
	}
}

func target(goos, goarch string) source.Target {
	return source.Target{
		Owner:      "helm",
		Repository: "helm",
		Artifact:   "helm",
		Platform:   platformpkg.Platform{OS: goos, Arch: goarch},
	}
}
