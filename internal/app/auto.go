package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/krisiasty/ghget/internal/archive"
	"github.com/krisiasty/ghget/internal/autoselect"
	gh "github.com/krisiasty/ghget/internal/github"
	"github.com/krisiasty/ghget/internal/install"
)

// autoSelect chooses the asset built for this host, reporting what it decided.
//
// An ambiguous or empty result is explained on stderr and then returned as an
// error: downloading the wrong program is worse than asking which one to use.
func (a *App) autoSelect(ctx context.Context, assets []gh.Asset, repo string, opts options) (string, error) {
	names := make([]string, len(assets))
	for i, asset := range assets {
		names[i] = asset.Name
	}
	result, err := autoselect.Select(names, a.platform, repo)
	a.debugSelection(ctx, result)

	var ambiguous *autoselect.AmbiguousError
	switch {
	case errors.As(err, &ambiguous):
		if opts.first {
			break
		}
		a.reportCandidates(result.Viable)
		return "", fmt.Errorf("%w; name one of them as OWNER/REPO/FILE, or use --first to take the top match", err)
	case errors.Is(err, autoselect.ErrNoMatch):
		a.reportRejections(result.Rejected)
		return "", fmt.Errorf("%w (%s); use --list to see every published asset", err, a.describePlatform())
	case err != nil:
		return "", err
	}

	selected := result.Viable[0]
	_, _ = fmt.Fprintf(a.stderr, "selected %s (%s)\n", selected.Name, selected.Reason)
	if opts.install && selected.Archive && !selected.Extractable {
		return "", fmt.Errorf("cannot install from %s: %s archives are not supported", selected.Name, filepath.Ext(selected.Name))
	}
	return selected.Name, nil
}

// describePlatform renders the host the way asset names describe it.
func (a *App) describePlatform() string {
	described := a.platform.OS + "/" + a.platform.Arch
	if libc := a.platform.Libc.String(); libc != "" {
		described += " " + libc
	}
	return described
}

// reportCandidates lists the assets that tied, so the user can pick one.
func (a *App) reportCandidates(candidates []autoselect.Candidate) {
	_, _ = fmt.Fprintln(a.stderr, "equally good candidates:")
	for _, candidate := range candidates {
		_, _ = fmt.Fprintf(a.stderr, "  %s (%s)\n", candidate.Name, candidate.Reason)
	}
}

// reportRejections explains why each published asset was ruled out.
func (a *App) reportRejections(candidates []autoselect.Candidate) {
	_, _ = fmt.Fprintln(a.stderr, "no candidate matched:")
	for _, candidate := range candidates {
		_, _ = fmt.Fprintf(a.stderr, "  %s: %s\n", candidate.Name, candidate.Reason)
	}
}

// debugSelection records the full ranking when --debug is enabled.
func (a *App) debugSelection(ctx context.Context, result autoselect.Result) {
	if a.logger == nil {
		return
	}
	for i, candidate := range result.Viable {
		a.debug(ctx, "asset ranked", "rank", i+1, "asset", candidate.Name, "reason", candidate.Reason)
	}
	for _, candidate := range result.Rejected {
		a.debug(ctx, "asset rejected", "asset", candidate.Name, "reason", candidate.Reason)
	}
}

// installAsset places the programs a downloaded asset contains into the
// destination directory, discarding documentation and other archive contents.
func (a *App) installAsset(downloaded string, asset gh.Asset, opts options) ([]string, error) {
	// The downloaded file is created on the destination filesystem so it can be
	// moved atomically. Keep staging there too: renaming a bare binary into the
	// system temporary directory fails with EXDEV when the filesystems differ.
	staging, err := os.MkdirTemp(filepath.Dir(downloaded), ".ghget-install-")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	programs, err := a.stageAsset(downloaded, asset, staging)
	if err != nil {
		return nil, err
	}
	destination, err := installDestination(programs, opts)
	if err != nil {
		return nil, err
	}
	placed, err := install.Place(programs, destination, opts.force)
	for _, path := range placed {
		_, _ = fmt.Fprintf(a.stderr, "installed %s\n", path)
	}
	if err != nil {
		return placed, err
	}
	if opts.output != "" && len(placed) == 1 {
		renamed, err := renameInstalled(placed[0], opts)
		if err != nil {
			return placed, err
		}
		placed[0] = renamed
	}
	return placed, nil
}

// stageAsset unpacks an archive, or stages a bare executable under the name of
// the program it holds, and returns the programs found.
func (a *App) stageAsset(downloaded string, asset gh.Asset, staging string) ([]string, error) {
	if !archive.Supported(asset.Name) {
		staged := filepath.Join(staging, autoselect.ProgramName(asset.Name))
		if err := os.Rename(downloaded, staged); err != nil {
			return nil, fmt.Errorf("stage %s: %w", asset.Name, err)
		}
		return []string{staged}, nil
	}
	if _, err := archive.Extract(downloaded, staging, asset.Name, archive.Options{Force: true}); err != nil {
		return nil, err
	}
	programs, err := install.Executables(staging, a.platform.OS)
	if err != nil {
		return nil, fmt.Errorf("install %s: %w", asset.Name, err)
	}
	return programs, nil
}

// installDestination reports where programs should be placed, honouring an
// --output path that names a single program.
func installDestination(programs []string, opts options) (string, error) {
	if opts.output == "" {
		return opts.directory, nil
	}
	if len(programs) != 1 {
		return "", fmt.Errorf("--output requires exactly one program to install (found %d)", len(programs))
	}
	target, err := outputPath(opts)
	if err != nil {
		return "", err
	}
	return filepath.Dir(target), nil
}

// renameInstalled gives a single installed program the name --output asked for.
func renameInstalled(placed string, opts options) (string, error) {
	target, err := outputPath(opts)
	if err != nil {
		return "", err
	}
	if target == placed {
		return placed, nil
	}
	if err := os.Rename(placed, target); err != nil {
		return "", fmt.Errorf("name installed program %s: %w", target, err)
	}
	return target, nil
}
