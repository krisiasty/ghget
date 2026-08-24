package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	want := []Asset{{"tool.zip", "https://github.test/acme/tool/releases/download/v1.2.3/tool.zip"}, {"checksums.txt", "https://github.test/acme/tool/releases/download/v1.2.3/checksums.txt"}}
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
