# ghget - smart release asset downloader - fetch, verify, extract. no token required

`ghget` downloads assets from public GitHub releases without authentication and
without calling `api.github.com`, avoiding rate-limits imposed on unauthenticated
users. Built-in aliases can also use other trusted release sources. It can
select several assets, verify their published checksums, and safely extract
common archive formats.

## Install a tool from its release

Name a built-in tool alias or a repository. `ghget` resolves the repository,
works out which asset was built for this machine, verifies its published
checksum, and leaves you with the program:

```sh
ghget uv --auto --install --dir ~/.local/bin
```

```text
resolved uv to astral-sh/uv
selected uv-x86_64-apple-darwin.tar.gz (darwin, amd64, tar.gz archive)
downloaded uv-x86_64-apple-darwin.tar.gz.sha256
downloaded uv-x86_64-apple-darwin.tar.gz
verified uv-x86_64-apple-darwin.tar.gz
installed ~/.local/bin/uv
installed ~/.local/bin/uvx
```

No filename to look up, no naming convention to learn, and nothing left over:
the archive's documentation, licences, and shell completions are discarded and
only the executables are kept. The same two flags work whatever shape a project
publishes its release in:

```sh
ghget BurntSushi/ripgrep --auto --install    # tarball beside docs    -> ./rg
ghget cli/cli --auto --install               # ZIP with a bin/ prefix -> ./gh
ghget kubernetes-sigs/kind --auto --install  # a bare binary          -> ./kind
ghget denoland/deno --auto --install         # ZIP, not denort's      -> ./deno
```

`--auto` will not guess across platforms. An asset built for another operating
system, architecture, or C library is rejected outright rather than ranked
lower, and when two assets are equally good `ghget` prints both and stops
instead of choosing for you. Selection is tested against release listings
captured from a hundred of the most popular repositories on GitHub.

The two flags are independent, and useful on their own: `--auto` selects the
asset and downloads it as published, while `--install` takes the programs out of
an asset you named yourself. [Automatic selection](#automatic-selection) and
[Installing programs](#installing-programs) describe each in full.

## Installing ghget

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

At startup, `ghget` checks for a newer release and prints an upgrade reminder
when one is available. Use `--skip-version-check` to omit this lookup for an
invocation.

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
ghget NAME[@TAG] --auto [--install]
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

# Extract only selected archive members, preserving their stored paths.
ghget owner/project/'project_linux_amd64.tar.gz' --extract \
  --file bin/project --file completions/project.zsh

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
| `-e` | `--extract` | Extract ZIP, TAR, TAR.GZ/TGZ, TAR.ZST/TZST, GZIP, or ZSTD |
| `-c VALUE` | `--checksum VALUE` | Verify against a digest or checksum file |
| `-x` | `--executable` | Add executable bits to downloaded/extracted regular files (mode `0755`) |
| `-u` | `--unquarantine` | Run `sudo xattr -dr com.apple.quarantine` on macOS |
| `-f` | `--force` | Overwrite existing downloaded or extracted files |
| `-k` | `--keep` | Keep the downloaded archive when using `--extract` |
| | `--flat` | Extract all files directly into the destination directory |
| | `--file ARCHIVE_PATH` | Extract one exact archive member; repeatable and requires `--extract` |
| | `--upgrade` | Replace the running `ghget` binary with the latest release |
| | `--skip-version-check` | Do not check for a newer `ghget` release at startup |
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

Supported archive formats are ZIP, TAR, TAR.GZ/TGZ, TAR.ZST/TZST, and single
files compressed with GZIP or Zstandard. Zstandard is decompressed in pure Go,
so cross-compiled builds need no C toolchain.

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

Use repeatable `--file ARCHIVE_PATH` options with `--extract` to extract only
the named regular files. Paths must exactly match the names stored in the
archive and are preserved unless `--flat` is also used. All requested members
are validated before extraction begins, so a missing member, non-regular member,
unsafe path, or flattened filename collision produces an error without writing
any selected files. Repeating the same `--file` value has no additional effect.

## Repository aliases

A built-in repository alias lets common tools be named without remembering its
owner or release source:

```sh
ghget fd --auto --install
ghget kubectl --auto --install
```

The resolved repository is always reported before release selection. Aliases
are case-insensitive, accept an explicit tag such as `fd@v10.2.0`, and work with
`--list` and `--tag` as well as `--auto`. An unknown alias stops with an error
that asks for an explicit `OWNER/REPO`; ghget never guesses a repository.

An alias may also provide an asset hint when one repository publishes separate
tools. For example, `kubens` resolves to `ahmetb/kubectx` while still selecting
the release asset whose product name is `kubens`.

Aliases may select a compiled-in release backend for projects that publish
outside GitHub Releases. For example, `kubectl` and `kubeadm` use `dl.k8s.io`,
including its mandatory SHA-256 sidecars. Registry entries can name only
backends implemented by ghget; they cannot supply arbitrary download URLs.

Explicit `OWNER/REPO` targets continue to bypass alias resolution. The curated
registry and contribution format are documented in
[`registry/README.md`](registry/README.md).

An alias does not guarantee that a compatible binary exists for every platform.
Unsupported combinations are reported before a download starts.

## Automatic selection

Selection reads each asset name rather than matching a list of known
conventions, of which there are too many to enumerate and which change without
notice.

Assets naming a different operating system or architecture are rejected
outright, as are checksum and signature sidecars, debugging symbols, `.deb`,
`.rpm`, `.msi`, `.pkg`, `.dmg`, AppImage, and source archives. An asset naming
no operating system is skipped when other assets in the release name theirs, so
debug bundles and application packages are not mistaken for builds. Whatever
survives is ranked, preferring a matching C library, an explicitly named
architecture, the asset carrying the project's own name, fewer unrecognised
words, a bare executable over an archive, and the archive format conventional
for the platform.

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
documentation, licences, manual pages, and shell completions. `uv` and `uvx`
land in the destination and nothing else, even though the published archive
stores them inside a `uv-x86_64-apple-darwin/` directory.

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

Use `--debug` to inspect requests, redirects, and downloads:

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

If no published checksum entry applies to a selected GitHub asset, `ghget`
falls back to the SHA-256 digest generated and displayed by GitHub for that
release asset. User-provided checksums take precedence where the selected
backend does not require its own published checksum. They cannot bypass a
backend's mandatory checksum policy.

Checksum manifests may contain `checksum filename`, `filename checksum`, BSD
`SHA256 (filename) = checksum`, or a single unnamed checksum.

## Release sources

Explicit `OWNER/REPO` targets and ordinary aliases use GitHub's public web
endpoints:

- `/OWNER/REPO/releases/latest` to resolve the latest tag via its redirect;
- `/OWNER/REPO/releases/expanded_assets/TAG` to enumerate assets;
- `/OWNER/REPO/releases/download/TAG/ASSET` to download an asset;
- `/OWNER/REPO.git/info/refs?service=git-upload-pack` to enumerate every tag in
  one Git smart-HTTP request.

This avoids API authentication and API rate limits, but GitHub can change these
public endpoints. Network and HTTP errors are reported directly.

The `kubectl` and `kubeadm` aliases use the fixed official Kubernetes layout:

- `/release/stable.txt` to resolve the current stable version;
- `/release/VERSION/bin/OS/ARCH/COMPONENT` to download a binary;
- the matching `.sha256` sidecar to verify it before installation.

Explicit versions such as `kubectl@v1.36.2` skip the stable-version lookup.

The `terraform` and `vault` aliases use `releases.hashicorp.com`. ghget discovers
stable versions from the official product index, confirms the requested
`PRODUCT_VERSION_OS_ARCH.zip` on its release page, and verifies it against the
mandatory `PRODUCT_VERSION_SHA256SUMS` manifest before extraction. Prerelease,
enterprise, and FIPS variants are excluded from automatic version selection.

Explicit versions accept either form, such as `terraform@1.10.2` or
`terraform@v1.10.2`; the leading `v` is normalized for HashiCorp's URL layout.
