package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/krisiasty/ghget/internal/archive"
	"github.com/krisiasty/ghget/internal/checksum"
	gh "github.com/krisiasty/ghget/internal/github"
	"github.com/krisiasty/ghget/internal/matcher"
)

const usage = `Usage:
  ghget OWNER/REPO/FILE_PATTERN[@TAG] [options]
  ghget OWNER/REPO[@TAG] --list
  ghget OWNER/REPO --tag

TAG defaults to "latest".

Options:
  -l, --list                 list release assets
  -t, --tag, --tags          list release tags
  -g, --glob                 treat FILE_PATTERN as a glob
  -r, --regex                treat FILE_PATTERN as a regular expression
  -d, --dir PATH             destination directory (default: current directory)
  -o, --output NAME          rename a single downloaded asset
  -e, --extract              extract ZIP, TAR, TAR.GZ/TGZ, or GZIP assets
  -c, --checksum VALUE       checksum digest or checksum-file path
  -x, --executable           make downloaded or extracted files executable
  -u, --unquarantine        remove macOS quarantine attributes using sudo
  -h, --help                 show this help
`

type releaseClient interface {
	ResolveLatest(context.Context, string, string) (string, error)
	ListAssets(context.Context, string, string, string) ([]gh.Asset, error)
	ListTags(context.Context, string, string) ([]string, error)
	Download(context.Context, gh.Asset) (io.ReadCloser, int64, error)
}

type App struct {
	client       releaseClient
	stdout       io.Writer
	stderr       io.Writer
	unquarantine func(context.Context, []string) error
}

func New(httpClient *http.Client, stdout, stderr io.Writer) *App {
	return &App{
		client:       gh.NewClient(httpClient),
		stdout:       stdout,
		stderr:       stderr,
		unquarantine: unquarantine,
	}
}

func NewWithClient(client releaseClient, stdout, stderr io.Writer) *App {
	return &App{client: client, stdout: stdout, stderr: stderr, unquarantine: unquarantine}
}

func (a *App) Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.help {
		_, err := io.WriteString(a.stdout, usage)
		return err
	}
	owner, repo, pattern, tag, err := parseTarget(opts.target)
	if err != nil {
		return err
	}

	if opts.listTags {
		if pattern != "" || tag != "latest" {
			return fmt.Errorf("--tag expects OWNER/REPO without a file or tag")
		}
		tags, err := a.client.ListTags(ctx, owner, repo)
		if err != nil {
			return err
		}
		for _, releaseTag := range tags {
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
			return fmt.Errorf("--list expects OWNER/REPO[@TAG] without a file")
		}
		for _, asset := range assets {
			if _, err := fmt.Fprintln(a.stdout, asset.Name); err != nil {
				return err
			}
		}
		return nil
	}
	if pattern == "" {
		return fmt.Errorf("missing file pattern; use --list to list assets")
	}

	names := make([]string, len(assets))
	byName := make(map[string]gh.Asset, len(assets))
	for i, asset := range assets {
		names[i] = asset.Name
		byName[asset.Name] = asset
	}
	selectedNames, err := matcher.Select(names, pattern, opts.mode)
	if err != nil {
		return err
	}
	if len(selectedNames) == 0 {
		return fmt.Errorf("no release assets match %q", pattern)
	}
	if opts.output != "" && len(selectedNames) != 1 {
		return fmt.Errorf("--output requires exactly one matching asset (matched %d)", len(selectedNames))
	}

	entries, err := a.checksums(ctx, assets, opts.checksum)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.directory, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	created := make([]string, 0)
	for _, name := range selectedNames {
		paths, err := a.downloadOne(ctx, byName[name], opts, entries)
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

func (a *App) checksums(ctx context.Context, assets []gh.Asset, provided string) ([]checksum.Entry, error) {
	if provided != "" {
		return checksum.ParseValueOrFile(provided)
	}
	entries := make([]checksum.Entry, 0)
	for _, asset := range assets {
		if !checksum.IsChecksumAsset(asset.Name) {
			continue
		}
		body, _, err := a.client.Download(ctx, asset)
		if err != nil {
			return nil, fmt.Errorf("download checksum asset: %w", err)
		}
		content, readErr := io.ReadAll(io.LimitReader(body, 16<<20))
		closeErr := body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read checksum asset %s: %w", asset.Name, readErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		parsed, err := checksum.Parse(bytes.NewReader(content))
		if err != nil {
			return nil, fmt.Errorf("parse checksum asset %s: %w", asset.Name, err)
		}
		if targetName := checksum.TargetFromSidecar(asset.Name); targetName != "" {
			for i := range parsed {
				if parsed[i].Filename == "" {
					parsed[i].Filename = targetName
				}
			}
		}
		entries = append(entries, parsed...)
	}
	return entries, nil
}

func (a *App) downloadOne(ctx context.Context, asset gh.Asset, opts options, entries []checksum.Entry) ([]string, error) {
	body, _, err := a.client.Download(ctx, asset)
	if err != nil {
		return nil, err
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
	if len(entries) > 0 {
		if err := checksum.VerifyFile(tmpPath, asset.Name, entries); err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(a.stderr, "verified %s\n", asset.Name)
	}

	if opts.extract {
		paths, err := archive.Extract(tmpPath, opts.directory, asset.Name)
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(a.stderr, "extracted %s\n", asset.Name)
		return paths, nil
	}
	outputName := asset.Name
	if opts.output != "" {
		outputName = opts.output
	}
	if filepath.Base(outputName) != outputName || outputName == "." || outputName == ".." {
		return nil, fmt.Errorf("output name must be a filename, not a path: %q", outputName)
	}
	destination := filepath.Join(opts.directory, outputName)
	if _, err := os.Lstat(destination); err == nil {
		return nil, fmt.Errorf("destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return nil, fmt.Errorf("save %s: %w", asset.Name, err)
	}
	_, _ = fmt.Fprintf(a.stderr, "downloaded %s\n", destination)
	return []string{destination}, nil
}

func parseTarget(target string) (owner, repo, pattern, tag string, err error) {
	tag = "latest"
	parts := strings.SplitN(target, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", fmt.Errorf("target must be OWNER/REPO[/FILE][@TAG]")
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
		return fmt.Errorf("--unquarantine is only supported on macOS")
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	args := append([]string{"xattr", "-dr", "com.apple.quarantine"}, paths...)
	cmd := exec.CommandContext(ctx, "sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remove quarantine attribute: %w", err)
	}
	return nil
}
