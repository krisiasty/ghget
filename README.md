# ghget

`ghget` downloads assets from public GitHub releases without authentication and
without calling `api.github.com`. It can select several assets, verify their
published checksums, and safely extract common archive formats.

## Install

Go 1.23 or newer is required to build from source:

```sh
go install github.com/krisiasty/ghget@latest
```

## Usage

```text
ghget OWNER/REPO/FILE_PATTERN[@TAG] [options]
ghget OWNER/REPO[@TAG] --list
ghget OWNER/REPO --tag
```

The tag defaults to `latest`. File matching is exact unless `--glob` or
`--regex` is selected. Use `{tag}` in the file pattern to insert the resolved
release tag. For Semantic Version tags, matching automatically tries both the
leading-`v` and unprefixed spellings.

```sh
# Download one asset from the latest release.
ghget cli/cli/gh_2.55.0_macOS_arm64.zip

# Download every Linux archive from a particular release.
ghget owner/project/'project_*_linux_*.tar.gz@v2.0.0' --glob

# Resolve latest, then try both tlsx_v1.3.0_... and tlsx_1.3.0_....
ghget projectdiscovery/tlsx/'tlsx_{tag}_macOS_amd64.zip'

# Extract a matching archive to ~/.local/bin and make extracted files executable.
ghget owner/project/'project_linux_amd64.tar.gz' --extract --dir ~/.local/bin --executable

# Discard paths inside the archive and put every file directly in ./tools.
ghget owner/project/'project_linux_amd64.tar.gz' --extract --flat --dir ./tools

# List assets or release tags.
ghget owner/project --list
ghget owner/project@v2.0.0 --list
ghget owner/project --tag
```

Tag listings always start with the synthetic `latest` selector. When every tag
is valid Semantic Versioning (with an optional leading `v`), tags are ordered
from highest to lowest SemVer precedence. If any tag is not SemVer, the entire
list uses ascending alphabetic order instead.

### Options

| Short | Long | Description |
|---|---|---|
| `-l` | `--list` | List assets for a release |
| `-t` | `--tag`, `--tags` | List release tags |
| `-g` | `--glob` | Treat the file pattern as a glob |
| `-r` | `--regex` | Treat the file pattern as a Go regular expression |
| `-d PATH` | `--dir PATH` | Choose the destination directory |
| `-o NAME` | `--output NAME` | Rename a single downloaded asset |
| `-e` | `--extract` | Extract ZIP, TAR, TAR.GZ/TGZ, or GZIP |
| `-c VALUE` | `--checksum VALUE` | Verify against a digest or checksum file |
| `-x` | `--executable` | Add executable bits to downloaded/extracted regular files |
| `-u` | `--unquarantine` | Run `sudo xattr -dr com.apple.quarantine` on macOS |
| `-f` | `--force` | Overwrite existing downloaded or extracted files |
| `-k` | `--keep` | Keep the downloaded archive when using `--extract` |
| | `--flat` | Extract all files directly into the destination directory |
| | `--debug` | Log structured HTTP telemetry to standard error |

Options may appear before or after the target. Existing files are never
overwritten unless `--force` is supplied.

When a download destination already exists without `--force`, `ghget` compares
it with the selected published or GitHub-generated checksum before downloading
the release asset. A matching file is kept and treated as a successful no-op. A
mismatch reports that the local file differs and suggests `--force`; if no
checksum is available, the tool reports that it cannot safely compare the file.

Archives are normally held in a temporary file and removed after successful
extraction. Add `--keep` to retain each archive under its original asset name in
the destination directory. `--keep` requires `--extract`, and `--force` also
controls whether a retained archive may replace an existing file.

Without `--force`, an extracted file that already exists is compared byte for
byte with the archive entry. Identical files are kept and extraction continues;
different files produce an error suggesting `--force`. Use `--flat` to discard
directories stored in the archive and place every regular file directly in the
destination directory. If flattened entries have the same filename, identical
content is accepted while differing content requires `--force` (the later entry
wins when forced). Both `--flat` and `--keep` require `--extract`.

### Debugging slow requests

Use `--debug` to see activity immediately during operations that require many
paginated requests:

```sh
ghget astral-sh/uv --tags --debug
```

Debug output is written to standard error using Go's `slog` text format. Every
HTTP exchange—including redirects—reports its method, URL, request and operation
IDs, status, response bytes, declared content length, content type, time to
response headers, and total response-body time. Authentication-like query values
in redirected download URLs are redacted.

## Checksums

When a release contains a checksum asset (for example `checksums.txt`,
`SHA256SUMS`, or `tool.tar.gz.sha256`), `ghget` fetches it automatically and
verifies selected assets before saving or extracting them. `--checksum` accepts
either a literal MD5, SHA-1, SHA-256, or SHA-512 digest, or a path to a checksum
file. Digest type is inferred from digest length.

For per-asset checksum sidecars, only sidecars belonging to selected files are
fetched. Shared checksum manifests are fetched when needed. If a checksum asset
itself matches the requested file pattern, it is saved from the already-fetched
content and verified with GitHub's generated digest when available, rather than
recursively requiring another checksum file.

If no published checksum entry applies to a selected asset, `ghget` falls back
to the SHA-256 digest generated and displayed by GitHub for that release asset.
User-provided checksums take precedence over both published files and GitHub's
generated digest.

Checksum manifests may contain `checksum filename`, `filename checksum`, BSD
`SHA256 (filename) = checksum`, or a single unnamed checksum.

## GitHub access

Discovery uses GitHub's public web endpoints:

- `/OWNER/REPO/releases/latest` to resolve the latest tag via its redirect;
- `/OWNER/REPO/releases/expanded_assets/TAG` to enumerate assets;
- `/OWNER/REPO/releases/download/TAG/ASSET` to download an asset;
- `/OWNER/REPO.git/info/refs?service=git-upload-pack` to enumerate every tag in
  one Git smart-HTTP request, with paginated `/releases` HTML as a fallback.

This avoids API authentication and API rate limits, but GitHub can change these
HTML endpoints. Network and HTTP errors are reported directly.
