// Package source defines release artifacts and the backend contract used to
// discover and download them.
package source

import (
	"context"
	"io"

	"github.com/krisiasty/ghget/internal/platform"
)

// Asset describes one downloadable release artifact.
type Asset struct {
	Name             string
	URL              string
	Digest           string
	ChecksumRequired bool
}

// Target identifies a project artifact and the platform it must run on.
type Target struct {
	Owner      string
	Repository string
	Artifact   string
	Platform   platform.Platform
}

// Backend discovers and downloads releases from one trusted source.
type Backend interface {
	ResolveLatest(context.Context, Target) (string, error)
	ListAssets(context.Context, Target, string) ([]Asset, error)
	ListTags(context.Context, Target) ([]string, error)
	Download(context.Context, Asset) (io.ReadCloser, int64, error)
}
