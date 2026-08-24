package github

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestReleaseDiscovery(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) *http.Response {
		switch r.URL.RequestURI() {
		case "/acme/tool/releases/latest":
			return response(http.StatusFound, "", map[string]string{"Location": "https://github.test/acme/tool/releases/tag/v1.2.3"})
		case "/acme/tool/releases/expanded_assets/v1.2.3":
			return response(http.StatusOK, `<a href="/acme/tool/releases/download/v1.2.3/tool.zip">tool.zip</a>
                    <clipboard-copy value="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"></clipboard-copy>
                    <a href='/acme/tool/releases/download/v1.2.3/checksums.txt'>checksums</a>`, nil)
		default:
			return response(http.StatusNotFound, "", nil)
		}
	})}
	client := NewClientWithBaseURL(httpClient, "https://github.test")
	tag, err := client.ResolveLatest(context.Background(), "acme", "tool")
	if err != nil || tag != "v1.2.3" {
		t.Fatalf("ResolveLatest() = %q, %v", tag, err)
	}
	assets, err := client.ListAssets(context.Background(), "acme", "tool", tag)
	if err != nil {
		t.Fatal(err)
	}
	want := []Asset{
		{Name: "tool.zip", URL: "https://github.test/acme/tool/releases/download/v1.2.3/tool.zip", Digest: strings.Repeat("a", 64)},
		{Name: "checksums.txt", URL: "https://github.test/acme/tool/releases/download/v1.2.3/checksums.txt"},
	}
	if !reflect.DeepEqual(assets, want) {
		t.Fatalf("ListAssets() = %#v, want %#v", assets, want)
	}
}

func TestListTagsFollowsPagination(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) *http.Response {
		switch r.URL.RequestURI() {
		case "/acme/tool/releases":
			return response(http.StatusOK, `<a href="/acme/tool/releases/tag/v2">v2</a><a rel="next" href="/acme/tool/releases?page=2">Next</a>`, nil)
		case "/acme/tool/releases?page=2":
			return response(http.StatusOK, `<a href="/acme/tool/releases/tag/v1">v1</a>`, nil)
		default:
			return response(http.StatusNotFound, "", nil)
		}
	})}
	client := NewClientWithBaseURL(httpClient, "https://github.test")
	tags, err := client.ListTags(context.Background(), "acme", "tool")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tags, []string{"v2", "v1"}) {
		t.Fatalf("ListTags() = %v", tags)
	}
}

func TestListTagsUsesGitRefAdvertisement(t *testing.T) {
	advertisement := pkt("# service=git-upload-pack\n") + "0000" +
		pkt(strings.Repeat("a", 40)+" HEAD\x00symref=HEAD:refs/heads/main\n") +
		pkt(strings.Repeat("b", 40)+" refs/tags/v2.0.0\n") +
		pkt(strings.Repeat("c", 40)+" refs/tags/release/v1\n") +
		pkt(strings.Repeat("d", 40)+" refs/tags/v2.0.0^{}\n") + "0000"
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) *http.Response {
		requests++
		if r.URL.RequestURI() != "/acme/tool.git/info/refs?service=git-upload-pack" {
			return response(http.StatusNotFound, "", nil)
		}
		return response(http.StatusOK, advertisement, nil)
	})}
	client := NewClientWithBaseURL(httpClient, "https://github.test")
	tags, err := client.ListTags(context.Background(), "acme", "tool")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tags, []string{"v2.0.0", "release/v1"}) {
		t.Fatalf("ListTags() = %v", tags)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestHTTPDebugTelemetry(t *testing.T) {
	const page = `<a href="/acme/tool/releases/tag/v1">v1</a>`
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) *http.Response {
		resp := response(http.StatusOK, page, map[string]string{"Content-Type": "text/html; charset=utf-8"})
		resp.ContentLength = int64(len(page))
		return resp
	})}
	client := NewClientWithBaseURL(httpClient, "https://github.test")
	var output strings.Builder
	client.SetLogger(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if _, err := client.ListTags(context.Background(), "acme", "tool"); err != nil {
		t.Fatal(err)
	}
	log := output.String()
	for _, want := range []string{
		`msg="http request"`,
		`msg="http response"`,
		`method=GET`,
		`url=https://github.test/acme/tool/releases`,
		`status_code=200`,
		fmt.Sprintf("content_length=%d", len(page)),
		fmt.Sprintf("response_bytes=%d", len(page)),
		`time_to_headers=`,
		`duration=`,
		`body_complete=true`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("debug output does not contain %q:\n%s", want, log)
		}
	}
}

func TestSafeURLRedactsCredentials(t *testing.T) {
	u, err := url.Parse("https://assets.test/file?jwt=secret&page=2&X-Amz-Signature=private")
	if err != nil {
		t.Fatal(err)
	}
	got := safeURL(u)
	if strings.Contains(got, "secret") || strings.Contains(got, "private") {
		t.Fatalf("safeURL leaked credentials: %s", got)
	}
	if !strings.Contains(got, "page=2") || !strings.Contains(got, "REDACTED") {
		t.Fatalf("safeURL removed useful query information: %s", got)
	}
}

type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r), nil
}

func response(status int, body string, headers map[string]string) *http.Response {
	header := make(http.Header)
	for name, value := range headers {
		header.Set(name, value)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func pkt(payload string) string {
	return fmt.Sprintf("%04x%s", len(payload)+4, payload)
}
