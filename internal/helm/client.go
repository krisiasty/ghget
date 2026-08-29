// Package helm discovers official Helm client archives published through
// get.helm.sh.
package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/krisiasty/ghget/internal/source"
	"github.com/krisiasty/ghget/internal/tagorder"
)

const (
	defaultReleaseBaseURL  = "https://get.helm.sh"
	defaultMetadataBaseURL = "https://api.github.com"
	latestVersionMarker    = "helm4-latest-version"
	maxLatestSize          = 128
	maxMetadataSize        = 16 << 20
	maxChecksumSize        = 4 << 10
	maxMetadataPages       = 10
	releasesPerPage        = 100
	minimumReleaseMajor    = 2
)

var (
	stableVersionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	assetNameRE     = regexp.MustCompile(
		`^helm-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-` +
			`(darwin-(amd64|arm64)|linux-(386|amd64|arm|arm64|loong64|ppc64le|s390x|riscv64)|windows-(amd64|arm64))` +
			`\.tar\.gz(\.sha256)?$`,
	)
)

type releasePlatform struct {
	os   string
	arch string
}

var supportedPlatforms = map[releasePlatform]struct{}{
	{os: "darwin", arch: "amd64"}:  {},
	{os: "darwin", arch: "arm64"}:  {},
	{os: "linux", arch: "386"}:     {},
	{os: "linux", arch: "amd64"}:   {},
	{os: "linux", arch: "arm"}:     {},
	{os: "linux", arch: "arm64"}:   {},
	{os: "linux", arch: "loong64"}: {},
	{os: "linux", arch: "ppc64le"}: {},
	{os: "linux", arch: "riscv64"}: {},
	{os: "linux", arch: "s390x"}:   {},
	{os: "windows", arch: "amd64"}: {},
	{os: "windows", arch: "arm64"}: {},
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Client downloads Helm archives from get.helm.sh and reads release metadata
// from Helm's public GitHub repository.
type Client struct {
	releaseBaseURL  string
	metadataBaseURL string
	http            *http.Client
	logger          *slog.Logger
}

// NewClient constructs a client for the official Helm release services.
func NewClient(httpClient *http.Client) *Client {
	return NewClientWithBaseURLs(httpClient, defaultReleaseBaseURL, defaultMetadataBaseURL)
}

// NewClientWithBaseURLs constructs a client for test or compatible endpoints.
func NewClientWithBaseURLs(httpClient *http.Client, releaseBaseURL, metadataBaseURL string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	releaseBaseURL = strings.TrimRight(releaseBaseURL, "/")
	metadataBaseURL = strings.TrimRight(metadataBaseURL, "/")
	clientCopy := *httpClient
	previousRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateRedirect(releaseBaseURL, metadataBaseURL, req.URL, via); err != nil {
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
	return &Client{
		releaseBaseURL:  releaseBaseURL,
		metadataBaseURL: metadataBaseURL,
		http:            &clientCopy,
	}
}

// SetLogger enables HTTP request telemetry through logger.
func (c *Client) SetLogger(logger *slog.Logger) {
	c.logger = logger
}

// ResolveLatest reads and validates Helm's current stable version marker.
func (c *Client) ResolveLatest(ctx context.Context, target source.Target) (string, error) {
	if err := validateTarget(target); err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/%s?ts=%d", c.releaseBaseURL, latestVersionMarker, time.Now().UnixNano())
	content, err := c.readPage(
		ctx,
		endpoint,
		"latest stable Helm version",
		maxLatestSize,
		http.Header{"Cache-Control": {"no-cache"}},
	)
	if err != nil {
		return "", fmt.Errorf("resolve latest Helm release: %w", err)
	}
	version, err := normalizeVersion(strings.TrimSpace(string(content)))
	if err != nil {
		return "", fmt.Errorf("invalid latest stable Helm version: %w", err)
	}
	return version, nil
}

// ListTags reports published stable Helm releases using GitHub's public
// release metadata. Metadata requests do not require authentication.
func (c *Client) ListTags(ctx context.Context, target source.Target) ([]string, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	versions := make([]string, 0, releasesPerPage)
	for page := 1; page <= maxMetadataPages; page++ {
		endpoint := fmt.Sprintf(
			"%s/repos/helm/helm/releases?per_page=%d&page=%d",
			c.metadataBaseURL,
			releasesPerPage,
			page,
		)
		content, err := c.readPage(
			ctx,
			endpoint,
			"Helm GitHub release metadata",
			maxMetadataSize,
			http.Header{
				"Accept":               {"application/vnd.github+json"},
				"X-GitHub-Api-Version": {"2022-11-28"},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("list Helm releases: %w", err)
		}
		var releases []githubRelease
		if err := json.Unmarshal(content, &releases); err != nil {
			return nil, fmt.Errorf("decode Helm GitHub release metadata: %w", err)
		}
		for _, release := range releases {
			if release.Draft || release.Prerelease {
				continue
			}
			version, err := normalizeVersion(release.TagName)
			if err != nil {
				continue
			}
			if _, found := seen[version]; found {
				continue
			}
			seen[version] = struct{}{}
			versions = append(versions, version)
		}
		if len(releases) < releasesPerPage {
			return sortVersions(versions), nil
		}
	}
	return sortVersions(versions), nil
}

// ListAssets constructs the official archive and mandatory SHA-256 sidecar
// for a stable Helm version and supported platform.
func (c *Client) ListAssets(_ context.Context, target source.Target, version string) ([]source.Asset, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	version, err := normalizeVersion(version)
	if err != nil {
		return nil, err
	}
	platform := releasePlatform{os: target.Platform.OS, arch: target.Platform.Arch}
	if _, supported := supportedPlatforms[platform]; !supported {
		return nil, fmt.Errorf("helm does not publish releases for %s/%s", target.Platform.OS, target.Platform.Arch)
	}
	archiveName := fmt.Sprintf("helm-%s-%s-%s.tar.gz", version, platform.os, platform.arch)
	archiveURL := c.releaseBaseURL + "/" + archiveName
	return []source.Asset{
		{Name: archiveName, URL: archiveURL, ChecksumRequired: true},
		{Name: archiveName + ".sha256", URL: archiveURL + ".sha256"},
	}, nil
}

// Download opens one asset previously constructed by ListAssets.
func (c *Client) Download(ctx context.Context, asset source.Asset) (io.ReadCloser, int64, error) {
	if err := c.validateAssetURL(asset.URL); err != nil {
		return nil, 0, err
	}
	if strings.HasSuffix(asset.Name, ".sha256") {
		content, err := c.readPage(ctx, asset.URL, asset.Name, maxChecksumSize, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("download Helm checksum %s: %w", asset.Name, err)
		}
		return io.NopCloser(bytes.NewReader(content)), int64(len(content)), nil
	}
	body, size, err := c.download(ctx, asset.URL, asset.Name, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("download Helm asset %s: %w", asset.Name, err)
	}
	return body, size, nil
}

func (c *Client) readPage(
	ctx context.Context,
	endpoint, description string,
	limit int64,
	headers http.Header,
) ([]byte, error) {
	body, _, err := c.download(ctx, endpoint, description, headers)
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

func (c *Client) download(
	ctx context.Context,
	endpoint, description string,
	headers http.Header,
) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
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

func (c *Client) validateAssetURL(value string) error {
	asset, err := url.Parse(value)
	if err != nil || validateReleaseURL(c.releaseBaseURL, asset) != nil || !assetNameRE.MatchString(pathBase(c.releaseBaseURL, asset)) {
		return errors.New("refusing Helm asset URL outside the configured release source")
	}
	return nil
}

func validateRedirect(releaseBaseURL, metadataBaseURL string, candidate *url.URL, via []*http.Request) error {
	if len(via) == 0 {
		return errors.New("cannot validate Helm redirect without an origin request")
	}
	origin := via[0].URL
	if validateReleaseURL(releaseBaseURL, origin) == nil {
		if validateReleaseURL(releaseBaseURL, candidate) != nil || !sameResource(origin, candidate) {
			return errors.New("URL is outside the configured Helm release source")
		}
		return nil
	}
	if validateMetadataURL(metadataBaseURL, origin) == nil {
		if validateMetadataURL(metadataBaseURL, candidate) != nil || !sameResource(origin, candidate) {
			return errors.New("URL is outside the configured Helm metadata source")
		}
		return nil
	}
	return errors.New("redirect originated outside the configured Helm sources")
}

func sameResource(left, right *url.URL) bool {
	return left != nil && right != nil && left.EscapedPath() == right.EscapedPath() && left.RawQuery == right.RawQuery
}

func validateReleaseURL(baseURL string, candidate *url.URL) error {
	base, err := url.Parse(baseURL)
	if err != nil || !sameOrigin(base, candidate) {
		return errors.New("URL is outside the configured Helm release source")
	}
	relative := pathBase(baseURL, candidate)
	if relative == latestVersionMarker {
		if validLatestQuery(candidate) {
			return nil
		}
		return errors.New("URL is outside the configured Helm release source")
	}
	if candidate.RawQuery != "" || !assetNameRE.MatchString(relative) {
		return errors.New("URL is outside the configured Helm release source")
	}
	return nil
}

func validateMetadataURL(baseURL string, candidate *url.URL) error {
	base, err := url.Parse(baseURL)
	if err != nil || !sameOrigin(base, candidate) {
		return errors.New("URL is outside the configured Helm metadata source")
	}
	wantedPath := strings.TrimRight(base.EscapedPath(), "/") + "/repos/helm/helm/releases"
	if candidate.EscapedPath() != wantedPath {
		return errors.New("URL is outside the configured Helm metadata source")
	}
	query := candidate.Query()
	if len(query) != 2 || len(query["per_page"]) != 1 || query.Get("per_page") != "100" ||
		len(query["page"]) != 1 || !decimal(query.Get("page"), 2) {
		return errors.New("URL is outside the configured Helm metadata source")
	}
	return nil
}

func sameOrigin(base, candidate *url.URL) bool {
	return base != nil && base.Scheme != "" && base.Host != "" && candidate != nil &&
		candidate.Scheme == base.Scheme && candidate.Host == base.Host && candidate.User == nil && candidate.Fragment == ""
}

func validLatestQuery(candidate *url.URL) bool {
	query := candidate.Query()
	return len(query) == 1 && len(query["ts"]) == 1 && decimal(query.Get("ts"), 20)
}

func decimal(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func pathBase(baseURL string, candidate *url.URL) string {
	base, err := url.Parse(baseURL)
	if err != nil || candidate == nil {
		return ""
	}
	prefix := strings.TrimRight(base.EscapedPath(), "/") + "/"
	if !strings.HasPrefix(candidate.EscapedPath(), prefix) {
		return ""
	}
	return strings.TrimPrefix(candidate.EscapedPath(), prefix)
}

func validateTarget(target source.Target) error {
	if target.Owner != "helm" || target.Repository != "helm" || target.Artifact != "helm" {
		return fmt.Errorf(
			"invalid Helm source target %q/%q for artifact %q",
			target.Owner,
			target.Repository,
			target.Artifact,
		)
	}
	return nil
}

func normalizeVersion(version string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	version = "v" + version
	if !stableVersionRE.MatchString(version) {
		return "", fmt.Errorf("invalid stable Helm version %q", strings.TrimPrefix(version, "v"))
	}
	majorText, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
	major, err := strconv.Atoi(majorText)
	if err != nil || major < minimumReleaseMajor {
		return "", fmt.Errorf("unsupported Helm major version %q", majorText)
	}
	return version, nil
}

func sortVersions(versions []string) []string {
	ordered := tagorder.Sort(versions)
	if len(ordered) > 0 && ordered[0] == "latest" {
		ordered = ordered[1:]
	}
	return ordered
}
