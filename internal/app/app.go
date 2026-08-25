// Package app implements ghget's command-line workflow.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/krisiasty/ghget/internal/archive"
	"github.com/krisiasty/ghget/internal/checksum"
	gh "github.com/krisiasty/ghget/internal/github"
	"github.com/krisiasty/ghget/internal/matcher"
	"github.com/krisiasty/ghget/internal/tagorder"
)

const usage = `Usage:
  ghget OWNER/REPO/FILE_PATTERN[@TAG] [options]
  ghget OWNER/REPO[@TAG] --list
  ghget OWNER/REPO --tag

TAG defaults to "latest". Use {tag} in FILE_PATTERN to insert the resolved tag.

Options:
  -l, --list                 list release assets
  -t, --tag, --tags          list release tags
  -g, --glob                 treat FILE_PATTERN as a glob
  -r, --regex                treat FILE_PATTERN as a regular expression
  -d, --dir PATH             destination directory (default: .); expands ~ and $HOME
  -o, --output NAME          rename a single downloaded asset
  -e, --extract              extract ZIP, TAR, TAR.GZ/TGZ, or GZIP assets
  -c, --checksum VALUE       checksum digest or checksum-file path
  -x, --executable           make downloaded or extracted files executable
  -u, --unquarantine        remove macOS quarantine attributes using sudo
  -f, --force                overwrite existing files
  -k, --keep                 keep the downloaded archive after extraction
      --flat                 extract all files directly into the destination directory
      --debug                log HTTP telemetry to stderr
  -h, --help                 show this help
`

type releaseClient interface {
	ResolveLatest(context.Context, string, string) (string, error)
	ListAssets(context.Context, string, string, string) ([]gh.Asset, error)
	ListTags(context.Context, string, string) ([]string, error)
	Download(context.Context, gh.Asset) (io.ReadCloser, int64, error)
}

// App coordinates release discovery, verification, downloads, and extraction.
type App struct {
	client       releaseClient
	stdout       io.Writer
	stderr       io.Writer
	unquarantine func(context.Context, []string) error
	logger       *slog.Logger
}

// New constructs an App backed by GitHub's public release endpoints.
func New(httpClient *http.Client, stdout, stderr io.Writer) *App {
	return &App{
		client:       gh.NewClient(httpClient),
		stdout:       stdout,
		stderr:       stderr,
		unquarantine: unquarantine,
	}
}

// NewWithClient constructs an App with a custom release client.
func NewWithClient(client releaseClient, stdout, stderr io.Writer) *App {
	return &App{client: client, stdout: stdout, stderr: stderr, unquarantine: unquarantine}
}

// Run parses arguments and executes the requested release operation.
func (a *App) Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.help {
		_, err := io.WriteString(a.stdout, usage)
		return err
	}
	if opts.debug {
		a.logger = slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
		if client, ok := a.client.(interface{ SetLogger(*slog.Logger) }); ok {
			client.SetLogger(a.logger)
		}
		a.logger.DebugContext(ctx, "debug telemetry enabled")
	}
	owner, repo, pattern, tag, err := parseTarget(opts.target)
	if err != nil {
		return err
	}

	if opts.listTags {
		if pattern != "" || tag != "latest" {
			return errors.New("--tag expects OWNER/REPO without a file or tag")
		}
		started := time.Now()
		a.debug(ctx, "listing release tags", "owner", owner, "repo", repo)
		tags, err := a.client.ListTags(ctx, owner, repo)
		if err != nil {
			return err
		}
		a.debug(ctx, "release tags listed", "owner", owner, "repo", repo, "tag_count", len(tags), "duration", time.Since(started))
		orderedTags := tagorder.Sort(tags)
		for _, releaseTag := range orderedTags {
			if _, err := fmt.Fprintln(a.stdout, releaseTag); err != nil {
				return err
			}
		}
		return nil
	}
	if tag == "latest" {
		tag, err = a.client.ResolveLatest(ctx, owner, repo)
		if err != nil {
			return err
		}
	}
	assets, err := a.client.ListAssets(ctx, owner, repo, tag)
	if err != nil {
		return err
	}
	if opts.listAssets {
		if pattern != "" {
			return errors.New("--list expects OWNER/REPO[@TAG] without a file")
		}
		for _, asset := range assets {
			if _, err := fmt.Fprintln(a.stdout, asset.Name); err != nil {
				return err
			}
		}
		return nil
	}
	if pattern == "" {
		return errors.New("missing file pattern; use --list to list assets")
	}

	names := make([]string, len(assets))
	byName := make(map[string]gh.Asset, len(assets))
	for i, asset := range assets {
		names[i] = asset.Name
		byName[asset.Name] = asset
	}
	selectedNames, err := selectAssetsForTag(names, pattern, tag, opts.mode)
	if err != nil {
		return err
	}
	if len(selectedNames) == 0 {
		return fmt.Errorf("no release assets match %q", pattern)
	}
	if opts.output != "" && len(selectedNames) != 1 {
		return fmt.Errorf("--output requires exactly one matching asset (matched %d)", len(selectedNames))
	}

	verification, err := a.checksums(ctx, assets, selectedNames, opts.checksum)
	if err != nil {
		return err
	}
	// Release artifacts are user-facing files and should remain accessible under the user's umask.
	if err := os.MkdirAll(opts.directory, 0o755); err != nil { //nolint:gosec // Conventional download directory permissions are intentional.
		return fmt.Errorf("create destination directory: %w", err)
	}
	skip, err := a.checkExistingDownloads(selectedNames, byName, opts, verification)
	if err != nil {
		return err
	}
	created := make([]string, 0)
	for _, name := range selectedNames {
		if skip[name] {
			continue
		}
		paths, err := a.downloadOne(ctx, byName[name], opts, verification)
		if err != nil {
			return err
		}
		created = append(created, paths...)
	}
	if opts.executable {
		for _, path := range created {
			if err := makeExecutable(path); err != nil {
				return err
			}
		}
	}
	if opts.unquarantine {
		if err := a.unquarantine(ctx, created); err != nil {
			return err
		}
	}
	return nil
}

func selectAssetsForTag(names []string, pattern, tag string, mode matcher.Mode) ([]string, error) {
	if !strings.Contains(pattern, "{tag}") {
		return matcher.Select(names, pattern, mode)
	}
	for _, variant := range tagorder.Variants(tag) {
		replacement := variant
		switch mode {
		case matcher.Glob:
			replacement = escapeGlob(replacement)
		case matcher.Regex:
			replacement = regexp.QuoteMeta(replacement)
		}
		expanded := strings.ReplaceAll(pattern, "{tag}", replacement)
		selected, err := matcher.Select(names, expanded, mode)
		if err != nil {
			return nil, err
		}
		if len(selected) > 0 {
			return selected, nil
		}
	}
	return nil, nil
}

func escapeGlob(value string) string {
	var escaped strings.Builder
	for _, char := range value {
		if strings.ContainsRune(`\\*?[`, char) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(char)
	}
	return escaped.String()
}

func (a *App) checkExistingDownloads(selected []string, assets map[string]gh.Asset, opts options, verification verificationData) (map[string]bool, error) {
	skip := make(map[string]bool)
	if opts.extract || opts.force {
		return skip, nil
	}
	for _, name := range selected {
		asset := assets[name]
		destination, err := downloadDestination(asset, opts)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(destination)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("destination is a directory: %s", destination)
		}
		if !verification.verifyAll && !verification.verify[name] {
			return nil, fmt.Errorf("destination already exists and no checksum is available for comparison: %s; use --force to overwrite", destination)
		}
		if err := checksum.VerifyFile(destination, asset.Name, verification.entries); err != nil {
			var mismatch *checksum.MismatchError
			if errors.As(err, &mismatch) {
				return nil, fmt.Errorf("destination already exists but does not match the remote checksum: %s: %w; use --force to overwrite", destination, err)
			}
			return nil, fmt.Errorf("destination already exists but could not be compared with the remote checksum: %s: %w; use --force to overwrite", destination, err)
		}
		skip[name] = true
		_, _ = fmt.Fprintf(a.stderr, "already exists and checksum matches %s\n", destination)
	}
	return skip, nil
}

func (a *App) debug(ctx context.Context, message string, args ...any) {
	if a.logger != nil {
		a.logger.DebugContext(ctx, message, args...)
	}
}

type verificationData struct {
	entries   []checksum.Entry
	fetched   map[string][]byte
	verify    map[string]bool
	verifyAll bool
}

func (a *App) checksums(ctx context.Context, assets []gh.Asset, selected []string, provided string) (verificationData, error) {
	if provided != "" {
		entries, err := checksum.ParseValueOrFile(provided)
		return verificationData{entries: entries, verifyAll: true}, err
	}
	data := verificationData{fetched: make(map[string][]byte), verify: make(map[string]bool)}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		if !checksum.IsChecksumAsset(name) {
			wanted[name] = true
		}
	}
	sources := make([]gh.Asset, 0)
	sourceNames := make([]string, 0)
	for _, asset := range assets {
		if !checksum.IsChecksumAsset(asset.Name) {
			continue
		}
		if target := checksum.TargetFromSidecar(asset.Name); target != "" && !wanted[target] {
			continue
		}
		sources = append(sources, asset)
		sourceNames = append(sourceNames, asset.Name)
	}
	a.debug(ctx, "automatic checksum sources selected", "source_count", len(sources), "sources", sourceNames)

	entries := make([]checksum.Entry, 0)
	for _, asset := range sources {
		body, _, err := a.client.Download(ctx, asset)
		if err != nil {
			return data, fmt.Errorf("download checksum asset: %w", err)
		}
		content, readErr := io.ReadAll(io.LimitReader(body, 16<<20))
		closeErr := body.Close()
		if readErr != nil {
			return data, fmt.Errorf("read checksum asset %s: %w", asset.Name, readErr)
		}
		if closeErr != nil {
			return data, closeErr
		}
		_, _ = fmt.Fprintf(a.stderr, "downloaded %s\n", asset.Name)
		parsed, err := checksum.Parse(bytes.NewReader(content))
		if err != nil {
			return data, fmt.Errorf("parse checksum asset %s: %w", asset.Name, err)
		}
		data.fetched[asset.Name] = content
		if targetName := checksum.TargetFromSidecar(asset.Name); targetName != "" {
			for i := range parsed {
				if parsed[i].Filename == "" {
					parsed[i].Filename = targetName
				}
			}
		}
		entries = append(entries, parsed...)
	}
	data.entries = entries
	fallbackNames := make([]string, 0)
	for _, name := range selected {
		if checksum.HasEntry(name, data.entries) {
			data.verify[name] = true
			continue
		}
		asset := findAsset(assets, name)
		if asset.Digest == "" {
			continue
		}
		data.entries = append(data.entries, checksum.Entry{Filename: name, Digest: asset.Digest})
		data.verify[name] = true
		fallbackNames = append(fallbackNames, name)
	}
	if len(fallbackNames) > 0 {
		a.debug(ctx, "using GitHub-generated checksum fallback", "asset_count", len(fallbackNames), "assets", fallbackNames)
	}
	return data, nil
}

func findAsset(assets []gh.Asset, name string) gh.Asset {
	for _, asset := range assets {
		if asset.Name == name {
			return asset
		}
	}
	return gh.Asset{}
}

func (a *App) downloadOne(ctx context.Context, asset gh.Asset, opts options, verification verificationData) ([]string, error) {
	var body io.ReadCloser
	downloaded := false
	if content, ok := verification.fetched[asset.Name]; ok {
		body = io.NopCloser(bytes.NewReader(content))
	} else {
		var err error
		body, _, err = a.client.Download(ctx, asset)
		if err != nil {
			return nil, err
		}
		downloaded = true
	}
	tmp, err := os.CreateTemp(opts.directory, ".ghget-*")
	if err != nil {
		_ = body.Close()
		return nil, fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	_, copyErr := io.Copy(tmp, body)
	closeFileErr := tmp.Close()
	closeBodyErr := body.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Name, copyErr)
	}
	if closeFileErr != nil {
		return nil, closeFileErr
	}
	if closeBodyErr != nil {
		return nil, closeBodyErr
	}
	if downloaded {
		_, _ = fmt.Fprintf(a.stderr, "downloaded %s\n", downloadDisplayPath(asset, opts))
	}
	shouldVerify := len(verification.entries) > 0 && (verification.verifyAll || verification.verify[asset.Name])
	if shouldVerify {
		if err := checksum.VerifyFile(tmpPath, asset.Name, verification.entries); err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(a.stderr, "verified %s\n", asset.Name)
	}

	if opts.extract {
		archiveDestination := filepath.Join(opts.directory, asset.Name)
		if opts.keep {
			if err := checkWritableDestination(archiveDestination, opts.force); err != nil {
				return nil, err
			}
		}
		results, extractErr := archive.Extract(tmpPath, opts.directory, asset.Name, archive.Options{
			Force: opts.force,
			Flat:  opts.flat,
		})
		paths := make([]string, 0, len(results)+1)
		for _, result := range results {
			displayPath := extractedDisplayPath(result.Path, opts.directory)
			if result.Written {
				_, _ = fmt.Fprintf(a.stderr, "extracted %s\n", displayPath)
			} else {
				_, _ = fmt.Fprintf(a.stderr, "already exists and content matches %s\n", displayPath)
			}
			paths = append(paths, result.Path)
		}
		if extractErr != nil {
			return nil, extractErr
		}
		if opts.keep {
			if err := saveTemporaryFile(tmpPath, archiveDestination, opts.force); err != nil {
				return nil, fmt.Errorf("keep archive %s: %w", asset.Name, err)
			}
			paths = append(paths, archiveDestination)
			_, _ = fmt.Fprintf(a.stderr, "kept archive %s\n", archiveDestination)
		}
		return paths, nil
	}
	destination, err := downloadDestination(asset, opts)
	if err != nil {
		return nil, err
	}
	if err := saveTemporaryFile(tmpPath, destination, opts.force); err != nil {
		return nil, fmt.Errorf("save %s: %w", asset.Name, err)
	}
	return []string{destination}, nil
}

func downloadDisplayPath(asset gh.Asset, opts options) string {
	if opts.extract {
		return filepath.Join(opts.directory, asset.Name)
	}
	destination, err := downloadDestination(asset, opts)
	if err != nil {
		return asset.Name
	}
	return destination
}

func extractedDisplayPath(path, destination string) string {
	root, err := filepath.Abs(destination)
	if err != nil {
		return path
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(destination, relative)
}

func checkWritableDestination(destination string, force bool) error {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !force {
		return fmt.Errorf("destination already exists: %s", destination)
	}
	if info.IsDir() {
		return fmt.Errorf("cannot overwrite directory: %s", destination)
	}
	return nil
}

func saveTemporaryFile(tmpPath, destination string, force bool) error {
	if err := checkWritableDestination(destination, force); err != nil {
		return err
	}
	if force {
		if _, err := os.Lstat(destination); err == nil {
			if err := os.Remove(destination); err != nil {
				return fmt.Errorf("remove existing destination %s: %w", destination, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(tmpPath, destination)
}

func downloadDestination(asset gh.Asset, opts options) (string, error) {
	outputName := asset.Name
	if opts.output != "" {
		outputName = opts.output
	}
	if filepath.Base(outputName) != outputName || outputName == "." || outputName == ".." {
		return "", fmt.Errorf("output name must be a filename, not a path: %q", outputName)
	}
	return filepath.Join(opts.directory, outputName), nil
}

func parseTarget(target string) (owner, repo, pattern, tag string, err error) {
	tag = "latest"
	parts := strings.SplitN(target, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", errors.New("target must be OWNER/REPO[/FILE][@TAG]")
	}
	owner = parts[0]
	repoPart := parts[1]
	if len(parts) == 3 {
		pattern = parts[2]
		if at := strings.LastIndex(pattern, "@"); at >= 0 {
			tag = pattern[at+1:]
			pattern = pattern[:at]
		}
	} else if at := strings.LastIndex(repoPart, "@"); at >= 0 {
		tag = repoPart[at+1:]
		repoPart = repoPart[:at]
	}
	repo = repoPart
	if owner == "." || owner == ".." || repo == "" || repo == "." || repo == ".." ||
		strings.ContainsAny(owner+repo, "\\@") || tag == "" || pattern == "" && len(parts) == 3 {
		return "", "", "", "", fmt.Errorf("invalid target %q", target)
	}
	return owner, repo, pattern, tag, nil
}

func makeExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := os.Chmod(path, info.Mode().Perm()|0o111); err != nil {
		return fmt.Errorf("make %s executable: %w", path, err)
	}
	return nil
}

func unquarantine(ctx context.Context, paths []string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("--unquarantine is only supported on macOS")
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	args := append([]string{"xattr", "-dr", "com.apple.quarantine"}, paths...)
	cmd := exec.CommandContext(ctx, "sudo", args...) //nolint:gosec // The executable is fixed and all path arguments originate from validated destinations.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remove quarantine attribute: %w", err)
	}
	return nil
}
