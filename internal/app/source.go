package app

import (
	"context"
	"fmt"
	"io"
	"net/http"

	gh "github.com/krisiasty/ghget/internal/github"
	"github.com/krisiasty/ghget/internal/hashicorp"
	"github.com/krisiasty/ghget/internal/helm"
	"github.com/krisiasty/ghget/internal/kubernetes"
	"github.com/krisiasty/ghget/internal/source"
)

// githubBackend adapts the existing GitHub client to the source-neutral
// backend contract without coupling GitHub discovery to platform metadata.
type githubBackend struct {
	client releaseClient
}

func (b githubBackend) ResolveLatest(ctx context.Context, target source.Target) (string, error) {
	return b.client.ResolveLatest(ctx, target.Owner, target.Repository)
}

func (b githubBackend) ListAssets(ctx context.Context, target source.Target, tag string) ([]source.Asset, error) {
	return b.client.ListAssets(ctx, target.Owner, target.Repository, tag)
}

func (b githubBackend) ListTags(ctx context.Context, target source.Target) ([]string, error) {
	return b.client.ListTags(ctx, target.Owner, target.Repository)
}

func (b githubBackend) Download(ctx context.Context, asset source.Asset) (io.ReadCloser, int64, error) {
	return b.client.Download(ctx, gh.Asset(asset))
}

func (a *App) backend(name string) (source.Backend, error) {
	if name == "" {
		return githubBackend{client: a.client}, nil
	}
	backend, found := a.backends[name]
	if !found {
		return nil, fmt.Errorf("release source %q is not available", name)
	}
	return backend, nil
}

func defaultBackends(httpClient *http.Client) map[string]source.Backend {
	return map[string]source.Backend{
		"kubernetes": kubernetes.NewClient(httpClient),
		"hashicorp":  hashicorp.NewClient(httpClient),
		"helm":       helm.NewClient(httpClient),
	}
}
