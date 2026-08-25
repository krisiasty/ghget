# ghget - smart GitHub asset downloader - fetch, verify, extract. no token required

`ghget` downloads assets from public GitHub releases without authentication and
without calling `api.github.com`, avoiding rate-limits imposed on unauthenticated users.
It can select several assets, verify their published checksums, and safely extract common archive formats.

## Install

Ready-to-use binaries are published on the
[releases page](https://github.com/krisiasty/ghget/releases/latest). Download
the file matching your platform and install it onto your `PATH`:

```sh
curl -L -o ghget https://github.com/krisiasty/ghget/releases/latest/download/ghget_VERSION_OS_ARCH
install -m 755 ghget ~/.local/bin/ghget
```

Homebrew on macOS:

```sh
brew install --cask krisiasty/tap/ghget
```

Go 1.27 or newer is required to build from source:

```sh
go install github.com/krisiasty/ghget@latest
```

Tagged releases use GoReleaser to publish raw `ghget_VERSION_OS_ARCH` binaries
for macOS, Linux, and Windows on AMD64 and ARM64. macOS releases also include
`.tar.gz` archives used by the Homebrew cask. Windows filenames include the
`.exe` extension.

Once installed, `ghget` upgrades itself:

```sh
ghget --upgrade
```

This finds the running binary, downloads the latest release built for the
current platform, verifies its published checksum, and replaces the binary in
place with mode `0755`. When the binary is already the latest release, nothing
is downloaded; add `--force` to reinstall it anyway. To install a different
version, use the ordinary options and name the destination directly:

```sh
ghget krisiasty/ghget/'ghget_{tag}_{os}_{arch}@v0.1.1' --output ~/.local/bin/ghget --executable --force
```

`--upgrade` follows a symlink and replaces the file it points at, leaving the
link alone. Before downloading anything it checks that the binary's directory is
writable and that the binary belongs to the current user, reporting what to do
instead when it is not. A Homebrew-installed binary is refused, because
overwriting it would leave Homebrew's records stale — run
`brew upgrade --cask ghget` for those. The previous binary is moved aside during
the swap and restored if the download or checksum fails.

## Usage

```text
ghget OWNER/REPO/FILE_PATTERN[@TAG] [options]
ghget OWNER/REPO[@TAG] --auto [--install]
ghget OWNER/REPO[@TAG] --list
ghget OWNER/REPO --tag
ghget --upgrade
ghget --version
```

The tag defaults to `latest`. File matching is exact unless `--glob` or
`--regex` is selected. Asset patterns support these placeholders:

| Placeholder | Matching value |
| --- | --- |
| `{tag}` | Resolved release tag; Semantic Versions try both with and without a leading `v` |
| `{owner}` | Repository owner: the first component of `OWNER/REPO` |
| `{project}` | Repository name: the second component of `OWNER/REPO` |
| `{repo}` | Repository name; equivalent to `{project}` |
| `{arch}` | Current architecture: `amd64`/`x86_64` or `arm64`/`aarch64` |
| `{os}` | Current OS: `linux`, `windows`/`win`, or `darwin`/`macos`/`macOS`/`mac`/`osx` |
| `{vendor}` | `unknown` on Linux, `pc` on Windows, or `apple` on macOS |

`{arch}`, `{os}`, and `{vendor}` must be separated from adjacent filename text
by `-` or `_`. A pattern edge or the dot before a file extension is also a
valid boundary. In glob mode, an adjacent `*` may consume the separator; the
matched asset must still contain a real `-` or `_` boundary.

```sh
# Download one asset from the latest release.
ghget cli/cli/gh_2.55.0_macOS_arm64.zip

# Download every Linux archive from a particular release.
ghget owner/project/'project_*_linux_*.tar.gz@v2.0.0' --glob

# Resolve latest, then try both tlsx_v1.3.0_... and tlsx_1.3.0_....
ghget projectdiscovery/tlsx/'tlsx_{tag}_macOS_amd64.zip'

# Let ghget find the asset for this platform, then install just the programs.
ghget astral-sh/uv --auto --install --dir ~/.local/bin

# Select the release asset for this repository, architecture, vendor, and OS.
ghget astral-sh/uv/'{repo}-{arch}-{vendor}-{os}.tar.gz'

# Insert the project name and select the current release and platform.
ghget projectdiscovery/naabu/'{project}*{tag}*{os}_{arch}.zip' --glob

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
| --- | --- | --- |
| `-a` | `--auto` | Select the asset built for this OS, CPU, and C library |
| `-i` | `--install` | Place only the programs an asset contains |
| | `--first` | Take the top match when `--auto` finds a tie |
| `-l` | `--list` | List assets for a release |
| `-t` | `--tag`, `--tags` | List release tags |
| `-g` | `--glob` | Treat the file pattern as a glob |
| `-r` | `--regex` | Treat the file pattern as a Go regular expression |
| `-d PATH` | `--dir PATH` | Choose the destination directory; expand `~` or `$HOME` |
| `-o PATH` | `--output PATH` | Write a single asset to this filename or path |
| `-e` | `--extract` | Extract ZIP, TAR, TAR.GZ/TGZ, or GZIP |
| `-c VALUE` | `--checksum VALUE` | Verify against a digest or checksum file |
| `-x` | `--executable` | Add executable bits to downloaded/extracted regular files (mode `0755`) |
| `-u` | `--unquarantine` | Run `sudo xattr -dr com.apple.quarantine` on macOS |
| `-f` | `--force` | Overwrite existing downloaded or extracted files |
| `-k` | `--keep` | Keep the downloaded archive when using `--extract` |
| | `--flat` | Extract all files directly into the destination directory |
| | `--upgrade` | Replace the running `ghget` binary with the latest release |
| | `--debug` | Log structured HTTP telemetry to standard error |
| | `--version` | Show version, commit, and build timestamp |

Options may appear before or after the target. Existing files are never
overwritten unless `--force` is supplied.

Downloaded assets and archives retained with `--keep` are written with mode
`0644`, because public release assets need no stricter permissions. Permissions
are applied after checksum verification, so a partial or unverified download is
never readable. `--executable` raises downloaded and extracted files to `0755`;
without it, extracted files keep the permissions recorded in the archive, so an
executable stored in a `.tar.gz` stays executable.

Missing destination directories and their parents are created automatically.
At the beginning of a `--dir` or `--output` path, `~`, `$HOME`, and `${HOME}`
resolve to the current user's home directory even when shell quoting prevents
expansion.

`--output` accepts a bare filename, a relative path resolved under `--dir`, or
an absolute path that stands on its own, so a single asset can be named and
placed in one option instead of two.

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

## Automatic selection

`--auto` picks the asset built for the current machine, so a project's naming
convention does not have to be known in advance:

```sh
ghget astral-sh/uv --auto
```

Selection reads each asset name rather than matching a list of known
conventions. Assets naming a different operating system or architecture are
rejected outright, as are checksum and signature sidecars, `.deb`, `.rpm`,
`.msi`, `.pkg`, `.dmg`, AppImage, and source archives. Whatever survives is
ranked, preferring a matching C library, an explicitly named architecture,
fewer unrecognised words in the name, a bare executable over an archive, and
the archive format conventional for the platform.

On Linux, `--auto` prefers a glibc build, or a musl build on a musl host such as
Alpine, detected from the loader in `/lib` or from `/etc/alpine-release`. A
glibc build is never selected on a musl host, because it cannot run there; a
musl build is accepted on a glibc host when nothing else is published. On
Windows, an MSVC build is preferred over a MinGW one. On macOS only the native
architecture is selected, along with universal builds; Rosetta is never assumed.

When two assets are equally good, `ghget` prints both and stops rather than
guessing. Name one directly, or add `--first` to take the top-ranked match:

```sh
ghget jqlang/jq --auto --first
```

When nothing matches, each asset is listed with the reason it was rejected. Add
`--debug` to see the full ranking for a successful selection too.

`--auto` selects the asset; it does not change what is written to disk. The
asset is downloaded as published, so `astral-sh/uv --auto` saves the `.tar.gz`.
Add `--extract` to unpack it in full, or `--install` to take just the programs.

## Installing programs

`--install` writes only the executables an asset contains, discarding
documentation, licences, manual pages, and shell completions:

```sh
ghget astral-sh/uv --auto --install --dir ~/.local/bin
```

That leaves `uv` and `uvx` in the destination and nothing else, even though the
published archive stores them inside a `uv-x86_64-apple-darwin/` directory. The
same command works for a project shipping a bare binary, an archive with a
`bin/` prefix, or a binary sitting beside its documentation.

Programs are recognised by the target platform rather than by inspecting the
archive: on Windows by the `.exe`, `.com`, `.bat`, or `.cmd` extension, because
a ZIP built with native Windows tooling records no permissions, and everywhere
else by the executable bit. Shared libraries are never treated as programs. If
an archive holds a `bin` directory, only its contents are installed; otherwise
the programs closest to the top of the archive are. Installed files are given
mode `0755`.

A bare binary is installed under the name of the program it holds, so
`kind-linux-amd64` is written as `kind`. Use `--output` to choose another name.

`--install` needs no `--auto`; it works with an explicit pattern too, and
`--keep` retains the downloaded archive beside the installed programs:

```sh
ghget owner/project/'project_*_linux_amd64.tar.gz' --glob --install --keep
```

An archive containing no executable is reported as an error rather than
unpacked, and `--install` cannot be combined with `--extract`, because both
decide what lands on disk.

### Debugging HTTP requests

Use `--debug` to inspect GitHub requests, redirects, and downloads:

```sh
ghget astral-sh/uv/'uv-{arch}-{vendor}-{os}.tar.gz' --debug
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
  one Git smart-HTTP request.

This avoids API authentication and API rate limits, but GitHub can change these
public endpoints. Network and HTTP errors are reported directly.
