package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/krisiasty/ghget/internal/buildinfo"
	"github.com/krisiasty/ghget/internal/tagorder"
)

// ghget upgrades itself from its own public releases.
const (
	upgradeOwner = "krisiasty"
	upgradeRepo  = "ghget"
)

// checkForUpdate returns the latest published release and warns when it is
// newer than current. Lookup failures are nonfatal for ordinary commands.
func (a *App) checkForUpdate(ctx context.Context, current string) (string, error) {
	if a.client == nil {
		return "", nil
	}
	tag, err := a.client.ResolveLatest(ctx, upgradeOwner, upgradeRepo)
	if err != nil {
		a.debug(ctx, "version check failed", "error", err)
		return "", nil
	}
	a.debug(ctx, "version check completed", "current", current, "latest", tag)
	if !tagorder.IsNewer(tag, current) {
		return tag, nil
	}
	_, err = fmt.Fprintf(a.stderr, "warning: ghget %s is available (current %s); run \"ghget --upgrade\" to update\n", tag, current)
	return tag, err
}

// upgrade replaces the running binary with the latest published release.
func (a *App) upgrade(ctx context.Context, opts options, tag string) error {
	path, err := a.executablePath()
	if err != nil {
		return fmt.Errorf("locate the running ghget binary: %w", err)
	}
	// A bin directory commonly holds a symlink to the real binary, and that
	// target is the file to replace.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	if homebrewManaged(resolved) {
		return fmt.Errorf("%s is managed by Homebrew; run \"brew upgrade --cask ghget\" instead", resolved)
	}
	if err := checkReplaceable(resolved); err != nil {
		return err
	}
	if tag == "" {
		tag, err = a.client.ResolveLatest(ctx, upgradeOwner, upgradeRepo)
		if err != nil {
			return err
		}
	}
	if !opts.force && isCurrentRelease(buildinfo.Version(), tag) {
		_, err := fmt.Fprintf(a.stdout, "ghget %s is already the latest release\n", buildinfo.Version())
		return err
	}
	return a.replaceBinary(ctx, resolved, tag, opts)
}

// replaceBinary moves the current binary aside, downloads its replacement, and
// restores the original if anything fails.
func (a *App) replaceBinary(ctx context.Context, resolved, tag string, opts options) error {
	aside := resolved + ".old"
	_ = os.Remove(aside)
	if err := os.Rename(resolved, aside); err != nil {
		return fmt.Errorf("move %s aside: %w", resolved, err)
	}
	target := fmt.Sprintf("%s/%s/%s@%s", upgradeOwner, upgradeRepo, upgradeAssetPattern(), tag)
	err := a.fetch(ctx, options{
		target:     target,
		directory:  filepath.Dir(resolved),
		output:     resolved,
		force:      true,
		executable: true,
		debug:      opts.debug,
	})
	if err != nil {
		if restoreErr := os.Rename(aside, resolved); restoreErr != nil {
			return fmt.Errorf("%w; the previous binary is at %s", err, aside)
		}
		return err
	}
	// Windows cannot unlink a running image, so the previous binary stays.
	if removeErr := os.Remove(aside); removeErr != nil {
		_, _ = fmt.Fprintf(a.stderr, "previous binary kept at %s\n", aside)
	}
	_, err = fmt.Fprintf(a.stdout, "upgraded ghget to %s at %s\n", tag, resolved)
	return err
}

// upgradeAssetPattern matches the release binary built for this platform.
func upgradeAssetPattern() string {
	pattern := "ghget_{tag}_{os}_{arch}"
	if runtime.GOOS == "windows" {
		pattern += ".exe"
	}
	return pattern
}

// isCurrentRelease reports whether the built-in version matches the release
// tag. A binary built from source reports "dev" and always upgrades.
func isCurrentRelease(version, tag string) bool {
	if version == "" || version == "dev" {
		return false
	}
	return strings.TrimPrefix(version, "v") == strings.TrimPrefix(tag, "v")
}

// homebrewManaged reports whether the binary sits inside a Homebrew prefix,
// where replacing it would leave Homebrew's own records stale.
func homebrewManaged(path string) bool {
	for _, segment := range strings.Split(path, string(os.PathSeparator)) {
		if segment == "Cellar" || segment == "Caskroom" {
			return true
		}
	}
	return false
}

// checkReplaceable verifies the binary can be swapped before anything is
// downloaded. Replacing it rewrites a directory entry, so the directory must be
// writable and the file must belong to the current user. The file itself is
// never opened for writing, because that reports ETXTBSY for a running binary.
func checkReplaceable(path string) error {
	directory := filepath.Dir(path)
	probe, err := os.CreateTemp(directory, ".ghget-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot replace %s: %s is not writable: %w; re-run with sudo or install ghget in a writable directory", path, directory, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("cannot replace %s: it belongs to another user; re-run with sudo", path)
	}
	return nil
}
