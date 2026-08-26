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
	"github.com/krisiasty/ghget/internal/buildinfo"
	"github.com/krisiasty/ghget/internal/checksum"
	gh "github.com/krisiasty/ghget/internal/github"
	"github.com/krisiasty/ghget/internal/matcher"
	"github.com/krisiasty/ghget/internal/platform"
	"github.com/krisiasty/ghget/internal/repoalias"
	"github.com/krisiasty/ghget/internal/source"
	"github.com/krisiasty/ghget/internal/tagorder"
)

// downloadFileMode is applied to downloaded assets and kept archives. Release
// assets come from public repositories, so conventional permissions apply.
const downloadFileMode = 0o644

const usage = `Usage:
  ghget OWNER/REPO/FILE_PATTERN[@TAG] [options]
  ghget NAME[@TAG] --auto [--install]
  ghget OWNER/REPO[@TAG] --auto [--install]
  ghget OWNER/REPO[@TAG] --list
  ghget OWNER/REPO --tag
  ghget --upgrade
  ghget --version

TAG defaults to "latest". FILE_PATTERN supports {tag}, {owner}, {project}, {repo},
{arch}, {os}, and {vendor}.

Options:
  -a, --auto                 select the asset built for this OS, CPU, and C library
  -i, --install              place only the programs an asset contains
      --first                take the top match when --auto finds a tie
  -l, --list                 list release assets
  -t, --tag, --tags          list release tags
  -g, --glob                 treat FILE_PATTERN as a glob
  -r, --regex                treat FILE_PATTERN as a regular expression
  -d, --dir PATH             destination directory (default: .); expands ~ and $HOME
  -o, --output PATH          write a single asset to this filename or path
  -e, --extract              extract ZIP, TAR, TAR.GZ/TGZ, TAR.ZST/TZST, GZIP, or ZSTD
  -c, --checksum VALUE       checksum digest or checksum-file path
  -x, --executable           make downloaded or extracted files executable
  -u, --unquarantine         remove macOS quarantine attributes using sudo
  -f, --force                overwrite existing files
  -k, --keep                 keep the downloaded archive after extraction
      --flat                 extract all files directly into the destination directory
      --upgrade              replace the running ghget binary with the latest release
      --debug                log HTTP telemetry to stderr
      --version              show version and build information
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
	backends     map[string]source.Backend
	stdout       io.Writer
	stderr       io.Writer
	unquarantine func(context.Context, []string) error
	// executablePath locates the running binary, replaced in tests.
	executablePath func() (string, error)
	logger         *slog.Logger
	// platform is the host --auto and --install match assets against.
	platform platform.Platform
}

// New constructs an App backed by GitHub's public release endpoints.
func New(httpClient *http.Client, stdout, stderr io.Writer) *App {
	return &App{
		client:       gh.NewClient(httpClient),
		backends:     defaultBackends(httpClient),
		stdout:       stdout,
		stderr:       stderr,
		unquarantine: unquarantine,

		executablePath: os.Executable,
		platform:       platform.Detect(),
	}
}

// NewWithClient constructs an App with a custom release client.
func NewWithClient(client releaseClient, stdout, stderr io.Writer) *App {
	return &App{
		client:         client,
		backends:       defaultBackends(nil),
		stdout:         stdout,
		stderr:         stderr,
		unquarantine:   unquarantine,
		executablePath: os.Executable,
		platform:       platform.Detect(),
	}
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
	if opts.version {
		_, err := fmt.Fprintln(a.stdout, buildinfo.String())
		return err
	}
	if opts.debug {
		a.logger = slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
		if client, ok := a.client.(interface{ SetLogger(*slog.Logger) }); ok {
			client.SetLogger(a.logger)
		}
		for _, backend := range a.backends {
			if client, ok := backend.(interface{ SetLogger(*slog.Logger) }); ok {
				client.SetLogger(a.logger)
			}
		}
		a.logger.DebugContext(ctx, "debug telemetry enabled")
	}
	if opts.upgrade {
		return a.upgrade(ctx, opts)
	}
	return a.fetch(ctx, opts)
}

// fetch resolves the release, selects assets, and downloads or extracts them.
func (a *App) fetch(ctx context.Context, opts options) error {
	resolution, err := resolveRepositoryAlias(opts.target)
	if err != nil {
		return err
	}
	if resolution.aliased {
		_, _ = fmt.Fprintf(a.stderr, "resolved %s to %s\n", opts.target, resolution.target)
	}
	owner, repo, pattern, tag, err := parseTarget(resolution.target)
	if err != nil {
		return err
	}
	backend, err := a.backend(resolution.backend)
	if err != nil {
		return err
	}
	artifact := repo
	if resolution.assetHint != "" {
		artifact = resolution.assetHint
	}
	sourceTarget := source.Target{
		Owner:      owner,
		Repository: repo,
		Artifact:   artifact,
		Platform:   a.platform,
	}

	if opts.listTags {
		if pattern != "" || tag != "latest" {
			return errors.New("--tag expects OWNER/REPO without a file or tag")
		}
		started := time.Now()
		a.debug(ctx, "listing release tags", "owner", owner, "repo", repo)
		tags, err := backend.ListTags(ctx, sourceTarget)
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
		tag, err = backend.ResolveLatest(ctx, sourceTarget)
		if err != nil {
			return err
		}
	}
	assets, err := backend.ListAssets(ctx, sourceTarget, tag)
	if err != nil {
		return err
	}
	if opts.auto && pattern != "" {
		return fmt.Errorf("--auto expects OWNER/REPO[@TAG] without a file pattern; drop --auto to download %q", pattern)
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
	if pattern == "" && !opts.auto {
		return errors.New("missing file pattern; use --auto to select one automatically, or --list to list assets")
	}

	names := make([]string, len(assets))
	byName := make(map[string]source.Asset, len(assets))
	for i, asset := range assets {
		names[i] = asset.Name
		byName[asset.Name] = asset
	}
	var selectedNames []string
	if opts.auto {
		selected, err := a.autoSelect(ctx, assets, artifact, opts)
		if err != nil {
			return err
		}
		selectedNames = []string{selected}
	} else {
		selectedNames, err = selectAssets(names, pattern, placeholderValues{
			tag:    tag,
			owner:  owner,
			repo:   repo,
			goos:   runtime.GOOS,
			goarch: runtime.GOARCH,
		}, opts.mode)
		if err != nil {
			return err
		}
		if len(selectedNames) == 0 {
			return fmt.Errorf("no release assets match %q", pattern)
		}
	}
	if opts.output != "" && len(selectedNames) != 1 {
		return fmt.Errorf("--output requires exactly one matching asset (matched %d)", len(selectedNames))
	}

	verification, err := a.checksums(ctx, backend, assets, selectedNames, opts.checksum)
	if err != nil {
		return err
	}
	if err := requireChecksums(selectedNames, byName, verification); err != nil {
		return err
	}
	// Release artifacts are user-facing files and should remain accessible under the user's umask.
	for _, directory := range destinationDirectories(opts) {
		if err := os.MkdirAll(directory, 0o755); err != nil { //nolint:gosec // Conventional download directory permissions are intentional.
			return fmt.Errorf("create destination directory: %w", err)
		}
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
		paths, err := a.downloadOne(ctx, backend, byName[name], opts, verification)
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

type placeholderValues struct {
	tag    string
	owner  string
	repo   string
	goos   string
	goarch string
}

type placeholderReplacement struct {
	placeholder string
	values      []string
}

func selectAssets(names []string, pattern string, values placeholderValues, mode matcher.Mode) ([]string, error) {
	for _, placeholder := range []string{"{arch}", "{os}", "{vendor}"} {
		if err := validatePlatformPlaceholder(pattern, placeholder, mode); err != nil {
			return nil, err
		}
	}

	tagVariants := []string{values.tag}
	if strings.Contains(pattern, "{tag}") {
		tagVariants = tagorder.Variants(values.tag)
	}
	for _, tag := range tagVariants {
		patterns := []string{pattern}
		for _, replacement := range []placeholderReplacement{
			{placeholder: "{tag}", values: []string{tag}},
			{placeholder: "{owner}", values: []string{values.owner}},
			{placeholder: "{project}", values: []string{values.repo}},
			{placeholder: "{repo}", values: []string{values.repo}},
			{placeholder: "{arch}", values: architectureVariants(values.goarch)},
			{placeholder: "{os}", values: operatingSystemVariants(values.goos)},
			{placeholder: "{vendor}", values: vendorVariants(values.goos)},
		} {
			patterns = expandPlaceholder(patterns, replacement, mode)
		}
		selected, err := selectMatchingAny(names, patterns, mode)
		if err != nil {
			return nil, err
		}
		selected = filterPlatformBoundaries(selected, pattern, values)
		if len(selected) > 0 {
			return selected, nil
		}
	}
	return nil, nil
}

func validatePlatformPlaceholder(pattern, placeholder string, mode matcher.Mode) error {
	for offset := 0; ; {
		index := strings.Index(pattern[offset:], placeholder)
		if index < 0 {
			return nil
		}
		index += offset
		after := index + len(placeholder)
		leftDelimited := index == 0 || pattern[index-1] == '-' || pattern[index-1] == '_' || mode == matcher.Regex && pattern[index-1] == '^' || mode == matcher.Glob && pattern[index-1] == '*'
		rightDelimited := after == len(pattern) || pattern[after] == '-' || pattern[after] == '_' || pattern[after] == '.' || strings.HasPrefix(pattern[after:], `\.`) || mode == matcher.Regex && pattern[after] == '$' || mode == matcher.Glob && pattern[after] == '*'
		if !leftDelimited || !rightDelimited {
			return fmt.Errorf("%s must be delimited by '-' or '_' in the asset pattern", placeholder)
		}
		offset = after
	}
}

func filterPlatformBoundaries(names []string, pattern string, values placeholderValues) []string {
	requirements := []placeholderReplacement{
		{placeholder: "{arch}", values: architectureVariants(values.goarch)},
		{placeholder: "{os}", values: operatingSystemVariants(values.goos)},
		{placeholder: "{vendor}", values: vendorVariants(values.goos)},
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		valid := true
		for _, requirement := range requirements {
			if strings.Contains(pattern, requirement.placeholder) && !containsDelimitedVariant(name, requirement.values) {
				valid = false
				break
			}
		}
		if valid {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func containsDelimitedVariant(name string, variants []string) bool {
	for _, variant := range variants {
		for offset := 0; ; {
			index := strings.Index(name[offset:], variant)
			if index < 0 {
				break
			}
			index += offset
			after := index + len(variant)
			leftDelimited := index == 0 || name[index-1] == '-' || name[index-1] == '_'
			rightDelimited := after == len(name) || name[after] == '-' || name[after] == '_' || name[after] == '.'
			if leftDelimited && rightDelimited {
				return true
			}
			offset = index + 1
		}
	}
	return false
}

func architectureVariants(goarch string) []string {
	switch goarch {
	case "amd64":
		return []string{"amd64", "x86_64"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	default:
		return []string{goarch}
	}
}

func operatingSystemVariants(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"darwin", "macos", "macOS", "mac", "osx"}
	case "windows":
		return []string{"windows", "win"}
	default:
		return []string{goos}
	}
}

func vendorVariants(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"apple"}
	case "windows":
		return []string{"pc"}
	default:
		return []string{"unknown"}
	}
}

func expandPlaceholder(patterns []string, replacement placeholderReplacement, mode matcher.Mode) []string {
	expanded := make([]string, 0, len(patterns)*len(replacement.values))
	for _, pattern := range patterns {
		if !strings.Contains(pattern, replacement.placeholder) {
			expanded = append(expanded, pattern)
			continue
		}
		for _, value := range replacement.values {
			expanded = append(expanded, strings.ReplaceAll(pattern, replacement.placeholder, escapePlaceholder(value, mode)))
		}
	}
	return expanded
}

func escapePlaceholder(value string, mode matcher.Mode) string {
	switch mode {
	case matcher.Glob:
		return escapeGlob(value)
	case matcher.Regex:
		return regexp.QuoteMeta(value)
	default:
		return value
	}
}

func selectMatchingAny(names, patterns []string, mode matcher.Mode) ([]string, error) {
	matched := make(map[string]bool, len(names))
	for _, pattern := range patterns {
		selected, err := matcher.Select(names, pattern, mode)
		if err != nil {
			return nil, err
		}
		for _, name := range selected {
			matched[name] = true
		}
	}
	selected := make([]string, 0, len(matched))
	for _, name := range names {
		if matched[name] {
			selected = append(selected, name)
		}
	}
	return selected, nil
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

func (a *App) checkExistingDownloads(
	selected []string,
	assets map[string]source.Asset,
	opts options,
	verification verificationData,
) (map[string]bool, error) {
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

func requireChecksums(selected []string, assets map[string]source.Asset, verification verificationData) error {
	for _, name := range selected {
		if !assets[name].ChecksumRequired || verification.verify[name] {
			continue
		}
		return fmt.Errorf("required checksum is unavailable for %s", name)
	}
	return nil
}

func (a *App) checksums(
	ctx context.Context,
	backend source.Backend,
	assets []source.Asset,
	selected []string,
	provided string,
) (verificationData, error) {
	var providedEntries []checksum.Entry
	if provided != "" {
		var err error
		providedEntries, err = checksum.ParseValueOrFile(provided)
		if err != nil {
			return verificationData{}, err
		}
		if !requiresSourceChecksum(assets, selected) {
			return verificationData{entries: providedEntries, verifyAll: true}, nil
		}
	}
	data := verificationData{fetched: make(map[string][]byte), verify: make(map[string]bool)}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		if !checksum.IsChecksumAsset(name) {
			wanted[name] = true
		}
	}
	sources := make([]source.Asset, 0)
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
		body, _, err := backend.Download(ctx, asset)
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
		if asset.ChecksumRequired || asset.Digest == "" {
			continue
		}
		data.entries = append(data.entries, checksum.Entry{Filename: name, Digest: asset.Digest})
		data.verify[name] = true
		fallbackNames = append(fallbackNames, name)
	}
	if len(fallbackNames) > 0 {
		a.debug(ctx, "using GitHub-generated checksum fallback", "asset_count", len(fallbackNames), "assets", fallbackNames)
	}
	data.entries = append(data.entries, providedEntries...)
	data.verifyAll = len(providedEntries) > 0
	return data, nil
}

func requiresSourceChecksum(assets []source.Asset, selected []string) bool {
	wanted := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		wanted[name] = struct{}{}
	}
	for _, asset := range assets {
		if _, selected := wanted[asset.Name]; selected && asset.ChecksumRequired {
			return true
		}
	}
	return false
}

func findAsset(assets []source.Asset, name string) source.Asset {
	for _, asset := range assets {
		if asset.Name == name {
			return asset
		}
	}
	return source.Asset{}
}

func (a *App) downloadOne(
	ctx context.Context,
	backend source.Backend,
	asset source.Asset,
	opts options,
	verification verificationData,
) ([]string, error) {
	var body io.ReadCloser
	downloaded := false
	if content, ok := verification.fetched[asset.Name]; ok {
		body = io.NopCloser(bytes.NewReader(content))
	} else {
		var err error
		body, _, err = backend.Download(ctx, asset)
		if err != nil {
			return nil, err
		}
		downloaded = true
	}
	tmp, err := os.CreateTemp(temporaryDirectory(asset, opts), ".ghget-*")
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

	if opts.install {
		paths, installErr := a.installAsset(tmpPath, asset, opts)
		if installErr != nil {
			return nil, installErr
		}
		if opts.keep {
			archiveDestination := filepath.Join(opts.directory, asset.Name)
			if err := saveTemporaryFile(tmpPath, archiveDestination, opts.force); err != nil {
				return nil, fmt.Errorf("keep archive %s: %w", asset.Name, err)
			}
			paths = append(paths, archiveDestination)
			_, _ = fmt.Fprintf(a.stderr, "kept archive %s\n", archiveDestination)
		}
		return paths, nil
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

func downloadDisplayPath(asset source.Asset, opts options) string {
	// An installed asset is unpacked from a temporary file, so naming a
	// destination path here would point at a file that never appears.
	if opts.install && !opts.keep {
		return asset.Name
	}
	if opts.extract || opts.install {
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
	// Downloaded assets carry no source mode, so apply conventional permissions
	// for public content only after verification and destination checks pass.
	if err := os.Chmod(tmpPath, downloadFileMode); err != nil {
		return fmt.Errorf("set permissions on %s: %w", destination, err)
	}
	return os.Rename(tmpPath, destination)
}

func downloadDestination(asset source.Asset, opts options) (string, error) {
	if opts.output != "" {
		return outputPath(opts)
	}
	if filepath.Base(asset.Name) != asset.Name || asset.Name == "." || asset.Name == ".." {
		return "", fmt.Errorf("asset name must be a filename, not a path: %q", asset.Name)
	}
	return filepath.Join(opts.directory, asset.Name), nil
}

// destinationDirectories lists the directories that must exist before writing.
// A path-valued --output can land outside --dir.
func destinationDirectories(opts options) []string {
	directories := []string{opts.directory}
	if opts.output == "" || opts.extract {
		return directories
	}
	destination, err := outputPath(opts)
	if err != nil {
		// The destination is validated again before use, which reports the error.
		return directories
	}
	if parent := filepath.Dir(destination); parent != opts.directory {
		directories = append(directories, parent)
	}
	return directories
}

// temporaryDirectory returns the directory holding the in-progress download.
// It must sit on the destination's filesystem, because the file is moved into
// place with a rename.
func temporaryDirectory(asset source.Asset, opts options) string {
	if opts.extract {
		return opts.directory
	}
	destination, err := downloadDestination(asset, opts)
	if err != nil {
		return opts.directory
	}
	return filepath.Dir(destination)
}

// outputPath resolves --output, which may name a bare filename, a relative path
// under --dir, or an absolute path that stands on its own.
func outputPath(opts options) (string, error) {
	output := opts.output
	if output == "." || output == ".." || os.IsPathSeparator(output[len(output)-1]) {
		return "", fmt.Errorf("output must include a filename: %q", output)
	}
	if filepath.IsAbs(output) {
		return filepath.Clean(output), nil
	}
	return filepath.Join(opts.directory, output), nil
}

type repositoryResolution struct {
	target    string
	assetHint string
	backend   string
	aliased   bool
}

// resolveRepositoryAlias expands a bare built-in alias while leaving an
// explicit OWNER/REPO target unchanged. A tag suffix is carried across.
func resolveRepositoryAlias(target string) (repositoryResolution, error) {
	if strings.Contains(target, "/") {
		return repositoryResolution{target: target}, nil
	}
	alias := target
	suffix := ""
	if at := strings.LastIndex(alias, "@"); at >= 0 {
		alias, suffix = alias[:at], alias[at:]
	}
	entry, found, err := repoalias.Lookup(alias)
	if err != nil {
		return repositoryResolution{}, fmt.Errorf("resolve repository alias: %w", err)
	}
	if !found {
		return repositoryResolution{}, fmt.Errorf("unknown repository alias %q; use OWNER/REPO", alias)
	}
	return repositoryResolution{
		target:    entry.Repository + suffix,
		assetHint: entry.AssetHint,
		backend:   entry.Backend,
		aliased:   true,
	}, nil
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
	if err := os.Chmod(path, info.Mode().Perm()|0o755); err != nil {
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
