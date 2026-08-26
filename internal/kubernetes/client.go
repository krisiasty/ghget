// Package kubernetes discovers official component binaries published through
// dl.k8s.io.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/krisiasty/ghget/internal/source"
)

const (
	defaultBaseURL = "https://dl.k8s.io"
	maxVersionSize = 128
)

var versionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type platform struct {
	os   string
	arch string
}

var componentPlatforms = map[string][]platform{
	"kubectl": {
		{os: "linux", arch: "amd64"},
		{os: "linux", arch: "arm64"},
		{os: "linux", arch: "ppc64le"},
		{os: "linux", arch: "s390x"},
		{os: "darwin", arch: "amd64"},
		{os: "darwin", arch: "arm64"},
		{os: "windows", arch: "amd64"},
		{os: "windows", arch: "arm64"},
	},
	"kubeadm": {
		{os: "linux", arch: "amd64"},
		{os: "linux", arch: "arm64"},
		{os: "linux", arch: "ppc64le"},
		{os: "linux", arch: "s390x"},
		{os: "windows", arch: "amd64"},
	},
}

// Client downloads Kubernetes releases from a fixed dl.k8s.io layout.
type Client struct {
	baseURL string
	http    *http.Client
	logger  *slog.Logger
}

// NewClient constructs a client for dl.k8s.io.
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

// ResolveLatest reads and validates the current stable Kubernetes version.
func (c *Client) ResolveLatest(ctx context.Context, target source.Target) (string, error) {
	if err := validateTarget(target); err != nil {
		return "", err
	}
	body, _, err := c.download(ctx, c.baseURL+"/release/stable.txt", "stable Kubernetes version")
	if err != nil {
		return "", fmt.Errorf("resolve latest Kubernetes release: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(body, maxVersionSize+1))
	closeErr := body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read stable Kubernetes version: %w", readErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if len(content) > maxVersionSize {
		return "", errors.New("stable Kubernetes version response is too large")
	}
	version := strings.TrimSpace(string(content))
	if err := validateVersion(version); err != nil {
		return "", fmt.Errorf("invalid stable Kubernetes version: %w", err)
	}
	return version, nil
}

// ListAssets constructs the binary and mandatory SHA-256 sidecar for the
// requested component, version, and platform.
func (c *Client) ListAssets(_ context.Context, target source.Target, version string) ([]source.Asset, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	wanted := platform{os: target.Platform.OS, arch: target.Platform.Arch}
	if !slices.Contains(componentPlatforms[target.Artifact], wanted) {
		return nil, fmt.Errorf(
			"kubernetes does not publish %s for %s/%s",
			target.Artifact,
			target.Platform.OS,
			target.Platform.Arch,
		)
	}
	extension := ""
	if target.Platform.OS == "windows" {
		extension = ".exe"
	}
	remoteName := target.Artifact + extension
	assetName := fmt.Sprintf("%s-%s-%s%s", target.Artifact, target.Platform.OS, target.Platform.Arch, extension)
	base := fmt.Sprintf(
		"%s/release/%s/bin/%s/%s/%s",
		c.baseURL,
		url.PathEscape(version),
		url.PathEscape(target.Platform.OS),
		url.PathEscape(target.Platform.Arch),
		url.PathEscape(remoteName),
	)
	return []source.Asset{
		{Name: assetName, URL: base, ChecksumRequired: true},
		{Name: assetName + ".sha256", URL: base + ".sha256"},
	}, nil
}

// ListTags reports the stable version known to dl.k8s.io. Arbitrary historical
// versions remain available through an explicit @VERSION target.
func (c *Client) ListTags(ctx context.Context, target source.Target) ([]string, error) {
	version, err := c.ResolveLatest(ctx, target)
	if err != nil {
		return nil, err
	}
	return []string{version}, nil
}

// Download opens one asset previously constructed by ListAssets.
func (c *Client) Download(ctx context.Context, asset source.Asset) (io.ReadCloser, int64, error) {
	if err := c.validateAssetURL(asset.URL); err != nil {
		return nil, 0, err
	}
	body, size, err := c.download(ctx, asset.URL, asset.Name)
	if err != nil {
		return nil, 0, fmt.Errorf("download Kubernetes asset %s: %w", asset.Name, err)
	}
	return body, size, nil
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

func (c *Client) validateAssetURL(value string) error {
	asset, assetErr := url.Parse(value)
	if assetErr != nil || asset.RawQuery != "" || asset.Fragment != "" || validateReleaseURL(c.baseURL, asset) != nil {
		return errors.New("refusing Kubernetes asset URL outside the configured release source")
	}
	return nil
}

func validateReleaseURL(baseURL string, asset *url.URL) error {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" || asset == nil || asset.Scheme != base.Scheme ||
		asset.Host != base.Host || asset.User != nil || asset.RawQuery != "" || asset.Fragment != "" ||
		!strings.HasPrefix(asset.EscapedPath(), strings.TrimRight(base.EscapedPath(), "/")+"/release/") {
		return errors.New("URL is outside the configured Kubernetes release source")
	}
	return nil
}

func validateTarget(target source.Target) error {
	if target.Owner != "kubernetes" {
		return fmt.Errorf("invalid Kubernetes source owner %q", target.Owner)
	}
	if _, supported := componentPlatforms[target.Artifact]; !supported {
		return fmt.Errorf("unsupported Kubernetes component %q", target.Artifact)
	}
	return nil
}

func validateVersion(version string) error {
	if !versionRE.MatchString(version) {
		return fmt.Errorf("invalid Kubernetes version %q", version)
	}
	return nil
}
