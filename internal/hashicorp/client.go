// Package hashicorp discovers official product binaries published through
// releases.hashicorp.com.
package hashicorp

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/krisiasty/ghget/internal/source"
	"github.com/krisiasty/ghget/internal/tagorder"
)

const (
	defaultBaseURL     = "https://releases.hashicorp.com"
	maxProductPageSize = 4 << 20
	maxVersionPageSize = 1 << 20
)

var (
	hrefRE          = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']+)["']`)
	platformPartRE  = regexp.MustCompile(`^[a-z0-9]+$`)
	stableVersionRE = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`,
	)
	supportedProducts = map[string]struct{}{
		"terraform": {},
		"vault":     {},
	}
)

// Client downloads HashiCorp products from the official release service.
type Client struct {
	baseURL string
	http    *http.Client
	logger  *slog.Logger
}

// NewClient constructs a client for releases.hashicorp.com.
func NewClient(httpClient *http.Client) *Client {
	return NewClientWithBaseURL(httpClient, defaultBaseURL)
}

// NewClientWithBaseURL constructs a client for a test or compatible endpoint.
func NewClientWithBaseURL(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	trimmedBaseURL := strings.TrimRight(baseURL, "/")
	clientCopy := *httpClient
	previousRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateReleaseURL(trimmedBaseURL, req.URL); err != nil {
			return err
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &Client{baseURL: trimmedBaseURL, http: &clientCopy}
}

// SetLogger enables HTTP request telemetry through logger.
func (c *Client) SetLogger(logger *slog.Logger) {
	c.logger = logger
}

// ResolveLatest discovers the newest stable release for a product.
func (c *Client) ResolveLatest(ctx context.Context, target source.Target) (string, error) {
	versions, err := c.ListTags(ctx, target)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no stable HashiCorp releases found for %s", target.Artifact)
	}
	return versions[0], nil
}

// ListTags discovers stable product versions from the official product index.
func (c *Client) ListTags(ctx context.Context, target source.Target) ([]string, error) {
	product, err := validateTarget(target)
	if err != nil {
		return nil, err
	}
	endpoint := c.productURL(product)
	page, err := c.readPage(ctx, endpoint, product+" release index", maxProductPageSize)
	if err != nil {
		return nil, fmt.Errorf("list HashiCorp %s releases: %w", product, err)
	}

	seen := make(map[string]struct{})
	versions := make([]string, 0)
	for _, link := range pageLinks(page, endpoint) {
		version, ok := c.productVersionFromURL(product, link)
		if !ok {
			continue
		}
		if _, found := seen[version]; found {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	ordered := tagorder.Sort(versions)
	if len(ordered) > 0 && ordered[0] == "latest" {
		ordered = ordered[1:]
	}
	return ordered, nil
}

// ListAssets confirms and returns the requested platform archive and its
// mandatory SHA-256 manifest.
func (c *Client) ListAssets(ctx context.Context, target source.Target, version string) ([]source.Asset, error) {
	product, err := validateTarget(target)
	if err != nil {
		return nil, err
	}
	version, err = normalizeVersion(version)
	if err != nil {
		return nil, err
	}
	if !platformPartRE.MatchString(target.Platform.OS) || !platformPartRE.MatchString(target.Platform.Arch) {
		return nil, fmt.Errorf("invalid HashiCorp platform %q", target.Platform.OS+"/"+target.Platform.Arch)
	}

	versionURL := c.versionURL(product, version)
	page, err := c.readPage(ctx, versionURL, product+" "+version+" release", maxVersionPageSize)
	if err != nil {
		return nil, fmt.Errorf("HashiCorp %s version %s is unavailable: %w", product, version, err)
	}
	archiveName := fmt.Sprintf("%s_%s_%s_%s.zip", product, version, target.Platform.OS, target.Platform.Arch)
	manifestName := fmt.Sprintf("%s_%s_SHA256SUMS", product, version)
	archiveURL := versionURL + url.PathEscape(archiveName)
	manifestURL := versionURL + url.PathEscape(manifestName)
	links := pageLinks(page, versionURL)
	if !containsURL(links, archiveURL) {
		return nil, fmt.Errorf(
			"HashiCorp does not publish %s %s for %s/%s",
			product,
			version,
			target.Platform.OS,
			target.Platform.Arch,
		)
	}
	if !containsURL(links, manifestURL) {
		return nil, fmt.Errorf("HashiCorp checksum manifest is unavailable for %s %s", product, version)
	}
	return []source.Asset{
		{Name: archiveName, URL: archiveURL, ChecksumRequired: true},
		{Name: manifestName, URL: manifestURL},
	}, nil
}

// Download opens one asset previously discovered by ListAssets.
func (c *Client) Download(ctx context.Context, asset source.Asset) (io.ReadCloser, int64, error) {
	if err := c.validateAssetURL(asset.URL); err != nil {
		return nil, 0, err
	}
	body, size, err := c.download(ctx, asset.URL, asset.Name)
	if err != nil {
		return nil, 0, fmt.Errorf("download HashiCorp asset %s: %w", asset.Name, err)
	}
	return body, size, nil
}

func (c *Client) readPage(ctx context.Context, endpoint, description string, limit int64) ([]byte, error) {
	body, _, err := c.download(ctx, endpoint, description)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(body, limit+1))
	closeErr := body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%s response is too large", description)
	}
	return content, nil
}

func (c *Client) download(ctx context.Context, endpoint, description string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	started := time.Now()
	if c.logger != nil {
		c.logger.DebugContext(ctx, "http request", "method", req.Method, "url", req.URL.Redacted())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if c.logger != nil {
		c.logger.DebugContext(
			ctx,
			"http response",
			"method",
			req.Method,
			"url",
			req.URL.Redacted(),
			"status",
			resp.StatusCode,
			"content_length",
			resp.ContentLength,
			"duration",
			time.Since(started),
		)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("%s returned %s", description, resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

func (c *Client) productURL(product string) string {
	return c.baseURL + "/" + url.PathEscape(product) + "/"
}

func (c *Client) versionURL(product, version string) string {
	return c.productURL(product) + url.PathEscape(version) + "/"
}

func (c *Client) productVersionFromURL(product, value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || validateReleaseURL(c.baseURL, parsed) != nil {
		return "", false
	}
	base, _ := url.Parse(c.baseURL)
	relative := strings.TrimPrefix(parsed.EscapedPath(), strings.TrimRight(base.EscapedPath(), "/")+"/")
	parts := strings.Split(strings.TrimSuffix(relative, "/"), "/")
	if len(parts) != 2 || parts[0] != product || !strings.HasSuffix(parsed.EscapedPath(), "/") {
		return "", false
	}
	version, err := url.PathUnescape(parts[1])
	return version, err == nil && stableVersionRE.MatchString(version)
}

func (c *Client) validateAssetURL(value string) error {
	asset, err := url.Parse(value)
	if err != nil || validateReleaseURL(c.baseURL, asset) != nil || !isAssetPath(c.baseURL, asset) {
		return errors.New("refusing HashiCorp asset URL outside the configured release source")
	}
	return nil
}

func pageLinks(page []byte, pageURL string) []string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	matches := hrefRE.FindAllSubmatch(page, -1)
	links := make([]string, 0, len(matches))
	for _, match := range matches {
		parsed, parseErr := url.Parse(html.UnescapeString(string(match[1])))
		if parseErr != nil {
			continue
		}
		links = append(links, base.ResolveReference(parsed).String())
	}
	return links
}

func containsURL(values []string, wanted string) bool {
	return slices.Contains(values, wanted)
}

func validateReleaseURL(baseURL string, candidate *url.URL) error {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" || candidate == nil || candidate.Scheme != base.Scheme ||
		candidate.Host != base.Host || candidate.User != nil || candidate.RawQuery != "" || candidate.Fragment != "" {
		return errors.New("URL is outside the configured HashiCorp release source")
	}
	prefix := strings.TrimRight(base.EscapedPath(), "/") + "/"
	if !strings.HasPrefix(candidate.EscapedPath(), prefix) || !validReleasePath(strings.TrimPrefix(candidate.EscapedPath(), prefix)) {
		return errors.New("URL is outside the configured HashiCorp release source")
	}
	return nil
}

func validReleasePath(relative string) bool {
	hasTrailingSlash := strings.HasSuffix(relative, "/")
	parts := strings.Split(strings.TrimSuffix(relative, "/"), "/")
	if len(parts) < 1 || len(parts) > 3 {
		return false
	}
	product, err := url.PathUnescape(parts[0])
	if err != nil {
		return false
	}
	if _, supported := supportedProducts[product]; !supported {
		return false
	}
	if len(parts) == 1 {
		return hasTrailingSlash
	}
	version, err := url.PathUnescape(parts[1])
	if err != nil || !stableVersionRE.MatchString(version) {
		return false
	}
	if len(parts) == 2 {
		return hasTrailingSlash
	}
	if hasTrailingSlash {
		return false
	}
	name, err := url.PathUnescape(parts[2])
	if err != nil {
		return false
	}
	return validAssetName(product, version, name)
}

func isAssetPath(baseURL string, candidate *url.URL) bool {
	base, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	prefix := strings.TrimRight(base.EscapedPath(), "/") + "/"
	return len(strings.Split(strings.TrimPrefix(candidate.EscapedPath(), prefix), "/")) == 3
}

func validAssetName(product, version, name string) bool {
	prefix := product + "_" + version + "_"
	if name == prefix+"SHA256SUMS" {
		return true
	}
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".zip") {
		return false
	}
	platform := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".zip")
	parts := strings.Split(platform, "_")
	return len(parts) == 2 && platformPartRE.MatchString(parts[0]) && platformPartRE.MatchString(parts[1])
}

func validateTarget(target source.Target) (string, error) {
	if target.Owner != "hashicorp" {
		return "", fmt.Errorf("invalid HashiCorp source owner %q", target.Owner)
	}
	product := target.Artifact
	if _, supported := supportedProducts[product]; !supported {
		return "", fmt.Errorf("unsupported HashiCorp product %q", product)
	}
	if target.Repository != product {
		return "", fmt.Errorf("invalid HashiCorp repository %q for product %q", target.Repository, product)
	}
	return product, nil
}

func normalizeVersion(version string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	if !stableVersionRE.MatchString(version) {
		return "", fmt.Errorf("invalid stable HashiCorp version %q", version)
	}
	return version, nil
}
