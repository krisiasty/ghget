package hashicorp

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

func TestListTagsFiltersAndSortsStableVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/terraform/" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, strings.Join([]string{
			`<a href="/terraform/1.9.8/">1.9.8</a>`,
			`<a href="/terraform/1.11.0-rc1/">1.11.0-rc1</a>`,
			`<a href="/terraform/1.10.2/">1.10.2</a>`,
			`<a href="/terraform/1.10.2+ent/">1.10.2+ent</a>`,
			`<a href="/vault/1.20.0/">wrong product</a>`,
			`<a href="https://example.test/terraform/9.9.9/">wrong host</a>`,
			`<a href="/terraform/1.10.2/">duplicate</a>`,
		}, "\n"))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	got, err := client.ListTags(context.Background(), target("terraform", "linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.10.2", "1.9.8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListTags() = %v, want %v", got, want)
	}
	latest, err := client.ResolveLatest(context.Background(), target("terraform", "linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if latest != "1.10.2" {
		t.Fatalf("ResolveLatest() = %q, want 1.10.2", latest)
	}
}

func TestListAssetsNormalizesVersionAndConfirmsFiles(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		if r.URL.Path != "/vault/1.19.3/" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, strings.Join([]string{
			`<a href="vault_1.19.3_darwin_arm64.zip">archive</a>`,
			`<a href="/vault/1.19.3/vault_1.19.3_SHA256SUMS">checksums</a>`,
		}, "\n"))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	assets, err := client.ListAssets(context.Background(), target("vault", "darwin", "arm64"), "v1.19.3")
	if err != nil {
		t.Fatal(err)
	}
	want := []source.Asset{
		{
			Name:             "vault_1.19.3_darwin_arm64.zip",
			URL:              server.URL + "/vault/1.19.3/vault_1.19.3_darwin_arm64.zip",
			ChecksumRequired: true,
		},
		{
			Name: "vault_1.19.3_SHA256SUMS",
			URL:  server.URL + "/vault/1.19.3/vault_1.19.3_SHA256SUMS",
		},
	}
	if !reflect.DeepEqual(assets, want) {
		t.Fatalf("ListAssets() = %#v, want %#v", assets, want)
	}
	if requested != "/vault/1.19.3/" {
		t.Fatalf("requested path = %q, want normalized version path", requested)
	}
}

func TestListAssetsReportsUnavailableInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/terraform/1.10.2/":
			_, _ = io.WriteString(w, `<a href="terraform_1.10.2_SHA256SUMS">checksums</a>`)
		case "/vault/1.19.3/":
			_, _ = io.WriteString(w, `<a href="vault_1.19.3_linux_amd64.zip">archive</a>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClientWithBaseURL(server.Client(), server.URL)

	tests := []struct {
		name    string
		target  source.Target
		version string
		want    string
	}{
		{
			name:    "platform",
			target:  target("terraform", "plan9", "amd64"),
			version: "1.10.2",
			want:    "does not publish terraform 1.10.2 for plan9/amd64",
		},
		{
			name:    "checksum manifest",
			target:  target("vault", "linux", "amd64"),
			version: "1.19.3",
			want:    "checksum manifest is unavailable",
		},
		{
			name:    "version",
			target:  target("terraform", "linux", "amd64"),
			version: "1.11.0-rc1",
			want:    "invalid stable HashiCorp version",
		},
		{
			name:    "product",
			target:  target("consul", "linux", "amd64"),
			version: "1.20.0",
			want:    "unsupported HashiCorp product",
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

func TestListTagsRejectsOversizedIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxProductPageSize+1))
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	_, err := client.ListTags(context.Background(), target("terraform", "linux", "amd64"))
	if err == nil || !strings.Contains(err.Error(), "response is too large") {
		t.Fatalf("ListTags() error = %v, want response size error", err)
	}
}

func TestDownloadRestrictsAndFetchesDiscoveredAssets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/terraform/1.10.2/":
			_, _ = io.WriteString(w, strings.Join([]string{
				`<a href="terraform_1.10.2_linux_amd64.zip">archive</a>`,
				`<a href="terraform_1.10.2_SHA256SUMS">checksums</a>`,
			}, "\n"))
		case "/terraform/1.10.2/terraform_1.10.2_linux_amd64.zip":
			_, _ = io.WriteString(w, "archive")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.Client(), server.URL)
	assets, err := client.ListAssets(context.Background(), target("terraform", "linux", "amd64"), "1.10.2")
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
	if string(content) != "archive" {
		t.Fatalf("downloaded content = %q, want archive", content)
	}

	_, _, err = client.Download(context.Background(), source.Asset{
		Name: "terraform.zip",
		URL:  server.URL + "/terraform/1.10.2/../../untrusted.zip",
	})
	if err == nil || !strings.Contains(err.Error(), "refusing HashiCorp asset URL") {
		t.Fatalf("Download() error = %v, want source restriction", err)
	}
}

func TestDownloadRejectsRedirectOutsideReleaseSource(t *testing.T) {
	var outsideRequests int
	var outsideURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/terraform/1.10.2/terraform_1.10.2_linux_amd64.zip" {
			http.Redirect(w, r, outsideURL+"/stolen", http.StatusFound)
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
	_, _, err := client.Download(context.Background(), source.Asset{
		Name: "terraform_1.10.2_linux_amd64.zip",
		URL:  server.URL + "/terraform/1.10.2/terraform_1.10.2_linux_amd64.zip",
	})
	if err == nil || !strings.Contains(err.Error(), "outside the configured HashiCorp release source") {
		t.Fatalf("Download() error = %v, want redirect restriction", err)
	}
	if outsideRequests != 0 {
		t.Fatalf("requests to redirected host = %d, want 0", outsideRequests)
	}
}

func target(product, goos, goarch string) source.Target {
	return source.Target{
		Owner:      "hashicorp",
		Repository: product,
		Artifact:   product,
		Platform:   platformpkg.Platform{OS: goos, Arch: goarch},
	}
}
