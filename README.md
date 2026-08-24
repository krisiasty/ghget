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
`--regex` is selected.

```sh
# Download one asset from the latest release.
ghget cli/cli/gh_2.55.0_macOS_arm64.zip

# Download every Linux archive from a particular release.
ghget owner/project/'project_*_linux_*.tar.gz@v2.0.0' --glob

# Extract a matching archive to ~/.local/bin and make extracted files executable.
ghget owner/project/'project_linux_amd64.tar.gz' --extract --dir ~/.local/bin --executable

# List assets or release tags.
ghget owner/project --list
ghget owner/project@v2.0.0 --list
ghget owner/project --tag
```

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

Options may appear before or after the target. Existing files are never
overwritten.

## Checksums

When a release contains a checksum asset (for example `checksums.txt`,
`SHA256SUMS`, or `tool.tar.gz.sha256`), `ghget` fetches it automatically and
verifies selected assets before saving or extracting them. `--checksum` accepts
either a literal MD5, SHA-1, SHA-256, or SHA-512 digest, or a path to a checksum
file. Digest type is inferred from digest length.

Checksum manifests may contain `checksum filename`, `filename checksum`, BSD
`SHA256 (filename) = checksum`, or a single unnamed checksum.

## GitHub access

Discovery uses GitHub's public web endpoints:

- `/OWNER/REPO/releases/latest` to resolve the latest tag via its redirect;
- `/OWNER/REPO/releases/expanded_assets/TAG` to enumerate assets;
- `/OWNER/REPO/releases/download/TAG/ASSET` to download an asset;
- paginated `/OWNER/REPO/releases` pages to enumerate release tags.

This avoids API authentication and API rate limits, but GitHub can change these
HTML endpoints. Network and HTTP errors are reported directly.
