package github

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

const defaultBaseURL = "https://github.com"

var anchorRE = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']([^"']+)["'][^>]*>`)
var generatedDigestRE = regexp.MustCompile(`(?i)\bvalue\s*=\s*["']sha256:([a-f0-9]{64})["']`)

type Asset struct {
	Name   string
	URL    string
	Digest string
}

type Client struct {
	baseURL         string
	http            *http.Client
	logging         *loggingTransport
	nextOperationID atomic.Uint64
}

func NewClient(httpClient *http.Client) *Client {
	return NewClientWithBaseURL(httpClient, defaultBaseURL)
}

func NewClientWithBaseURL(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clientCopy := *httpClient
	transport := clientCopy.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	logging := newLoggingTransport(transport)
	clientCopy.Transport = logging
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &clientCopy, logging: logging}
}

func (c *Client) SetLogger(logger *slog.Logger) {
	c.logging.SetLogger(logger)
}

func (c *Client) ResolveLatest(ctx context.Context, owner, repo string) (string, error) {
	endpoint := c.repoURL(owner, repo, "releases", "latest")
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.do(&client, req)
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("resolve latest release: expected HTTP 302, got %s", resp.Status)
	}

	location, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("resolve latest release: invalid Location header: %w", err)
	}
	tag, err := tagFromReleaseURL(location, owner, repo)
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	return tag, nil
}

func (c *Client) ListAssets(ctx context.Context, owner, repo, tag string) ([]Asset, error) {
	endpoint := c.repoURL(owner, repo, "releases", "expanded_assets", tag)
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("list assets for %s: %w", tag, err)
	}
	defer func() { _ = body.Close() }()

	b, err := io.ReadAll(io.LimitReader(body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("list assets for %s: %w", tag, err)
	}
	assetPrefix := "/" + owner + "/" + repo + "/releases/download/" + tag + "/"
	assets := make([]Asset, 0)
	seen := make(map[string]bool)
	anchorMatches := anchorRE.FindAllSubmatchIndex(b, -1)
	for i, match := range anchorMatches {
		href := html.UnescapeString(string(b[match[2]:match[3]]))
		u, err := url.Parse(href)
		if err != nil || !strings.HasPrefix(u.Path, assetPrefix) {
			continue
		}
		name := strings.TrimPrefix(u.Path, assetPrefix)
		if name == "" || strings.Contains(name, "/") || seen[name] {
			continue
		}
		seen[name] = true
		end := len(b)
		if i+1 < len(anchorMatches) {
			end = anchorMatches[i+1][0]
		}
		digest := ""
		if digestMatch := generatedDigestRE.FindSubmatch(b[match[1]:end]); digestMatch != nil {
			digest = strings.ToLower(string(digestMatch[1]))
		}
		assets = append(assets, Asset{Name: name, URL: c.absoluteURL(u.String()), Digest: digest})
	}
	return assets, nil
}

func (c *Client) ListTags(ctx context.Context, owner, repo string) ([]string, error) {
	tags, gitErr := c.listGitTags(ctx, owner, repo)
	if gitErr == nil {
		return tags, nil
	}
	tags, htmlErr := c.listReleaseTags(ctx, owner, repo)
	if htmlErr != nil {
		return nil, fmt.Errorf("list tags using Git refs (%v) and release pages (%w)", gitErr, htmlErr)
	}
	return tags, nil
}

func (c *Client) listGitTags(ctx context.Context, owner, repo string) ([]string, error) {
	endpoint := c.baseURL + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + ".git/info/refs?service=git-upload-pack"
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("read Git ref advertisement: %w", err)
	}
	defer func() { _ = body.Close() }()
	return parseGitTags(io.LimitReader(body, 128<<20))
}

func (c *Client) listReleaseTags(ctx context.Context, owner, repo string) ([]string, error) {
	next := c.repoURL(owner, repo, "releases")
	prefix := "/" + owner + "/" + repo + "/releases/tag/"
	seen := make(map[string]bool)
	seenPages := make(map[string]bool)
	tags := make([]string, 0)

	for next != "" {
		if seenPages[next] {
			return nil, fmt.Errorf("list release tags: pagination loop at %s", next)
		}
		seenPages[next] = true
		body, err := c.get(ctx, next)
		if err != nil {
			return nil, fmt.Errorf("list release tags: %w", err)
		}
		b, readErr := io.ReadAll(io.LimitReader(body, 16<<20))
		_ = body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("list release tags: %w", readErr)
		}

		next = ""
		for _, match := range anchorRE.FindAllSubmatch(b, -1) {
			href := html.UnescapeString(string(match[1]))
			u, err := url.Parse(href)
			if err != nil {
				continue
			}
			if strings.HasPrefix(u.Path, prefix) {
				tag := strings.TrimPrefix(u.Path, prefix)
				if tag != "" && !seen[tag] {
					seen[tag] = true
					tags = append(tags, tag)
				}
			}
			anchor := strings.ToLower(string(match[0]))
			if next == "" && (strings.Contains(anchor, `rel="next"`) || strings.Contains(anchor, `rel='next'`)) && c.sameRepositoryPage(u, owner, repo) {
				next = c.absoluteURL(u.String())
			}
		}
	}
	return tags, nil
}

func parseGitTags(r io.Reader) ([]string, error) {
	reader := bufio.NewReader(r)
	seenRefs := false
	seenTags := make(map[string]bool)
	tags := make([]string, 0)
	for {
		lengthBytes := make([]byte, 4)
		if _, err := io.ReadFull(reader, lengthBytes); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read Git packet length: %w", err)
		}
		length, err := strconv.ParseUint(string(lengthBytes), 16, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid Git packet length %q", lengthBytes)
		}
		if length == 0 || length == 1 || length == 2 {
			continue
		}
		if length < 4 {
			return nil, fmt.Errorf("invalid Git packet length %d", length)
		}
		payload := make([]byte, int(length)-4)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, fmt.Errorf("read Git packet: %w", err)
		}
		line := strings.TrimSuffix(string(payload), "\n")
		if strings.HasPrefix(line, "# service=") || line == "version 2" {
			continue
		}
		space := strings.IndexByte(line, ' ')
		if space < 0 {
			continue
		}
		seenRefs = true
		ref := line[space+1:]
		if nul := strings.IndexByte(ref, 0); nul >= 0 {
			ref = ref[:nul]
		}
		if !strings.HasPrefix(ref, "refs/tags/") || strings.HasSuffix(ref, "^{}") {
			continue
		}
		tag := strings.TrimPrefix(ref, "refs/tags/")
		if tag != "" && !seenTags[tag] {
			seenTags[tag] = true
			tags = append(tags, tag)
		}
	}
	if !seenRefs {
		return nil, fmt.Errorf("git server did not advertise refs")
	}
	return tags, nil
}

func (c *Client) Download(ctx context.Context, asset Asset) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.do(c.http, req)
	if err != nil {
		return nil, 0, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("download %s: %s", asset.Name, resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

func (c *Client) get(ctx context.Context, endpoint string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(c.http, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return resp.Body, nil
}

func (c *Client) do(client *http.Client, req *http.Request) (*http.Response, error) {
	operationID := c.nextOperationID.Add(1)
	ctx := context.WithValue(req.Context(), operationIDContextKey{}, operationID)
	return client.Do(req.WithContext(ctx))
}

func (c *Client) repoURL(owner, repo string, elems ...string) string {
	parts := append([]string{owner, repo}, elems...)
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return c.baseURL + "/" + strings.Join(parts, "/")
}

func (c *Client) absoluteURL(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if u.IsAbs() {
		return u.String()
	}
	base, _ := url.Parse(c.baseURL + "/")
	return base.ResolveReference(u).String()
}

func tagFromReleaseURL(location *url.URL, owner, repo string) (string, error) {
	prefix := "/" + owner + "/" + repo + "/releases/tag/"
	if !strings.HasPrefix(location.Path, prefix) {
		return "", fmt.Errorf("unexpected redirect to %s", location.String())
	}
	tag := strings.TrimPrefix(location.Path, prefix)
	if tag == "" {
		return "", fmt.Errorf("invalid tag in redirect to %s", location.String())
	}
	return tag, nil
}

func (c *Client) sameRepositoryPage(u *url.URL, owner, repo string) bool {
	base, err := url.Parse(c.baseURL)
	return err == nil && (u.Host == "" || u.Host == base.Host) && strings.HasPrefix(u.Path, "/"+owner+"/"+repo+"/releases")
}
