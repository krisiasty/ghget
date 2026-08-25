package autoselect

import (
	"regexp"
	"slices"
	"strings"
)

// Release assets encode platforms with a small, fairly stable vocabulary. These
// tables recognise that vocabulary; anything outside them is treated as an
// unrecognised token, which demotes a candidate rather than disqualifying it.

// osAliases maps an asset-name token to the GOOS value it denotes.
var osAliases = map[string]string{
	"linux": "linux", "darwin": "darwin", "macos": "darwin", "macosx": "darwin",
	"osx": "darwin", "mac": "darwin", "windows": "windows", "win": "windows",
	"freebsd": "freebsd", "openbsd": "openbsd", "netbsd": "netbsd",
	"dragonfly": "dragonfly", "solaris": "solaris", "illumos": "illumos",
	"aix": "aix", "android": "android", "ios": "ios", "plan9": "plan9",
	"js": "js", "wasip1": "wasip1", "wasm": "js",
}

// archAliases maps an asset-name token to the GOARCH value it denotes.
var archAliases = map[string]string{
	"amd64": "amd64", "x86_64": "amd64", "x64": "amd64", "64bit": "amd64",
	"386": "386", "i386": "386", "i686": "386", "x86": "386", "x32": "386",
	"ia32": "386", "32bit": "386",
	"arm64": "arm64", "aarch64": "arm64", "arm64e": "arm64",
	"arm": "arm", "armv5": "arm", "armv6": "arm", "armv7": "arm",
	"armv6l": "arm", "armv7l": "arm", "armhf": "arm", "armel": "arm",
	"ppc64le": "ppc64le", "powerpc64le": "ppc64le", "ppc64el": "ppc64le",
	"ppc64": "ppc64", "powerpc64": "ppc64", "powerpc": "ppc",
	"s390x": "s390x", "riscv64": "riscv64", "riscv64gc": "riscv64",
	"loong64": "loong64", "loongarch64": "loong64",
	"mips": "mips", "mipsel": "mipsle", "mipsle": "mipsle",
	"mips64": "mips64", "mips64el": "mips64le", "mips64le": "mips64le",
}

// universalArches denote a multi-architecture build that runs natively on the host.
var universalArches = map[string]bool{"universal": true, "universal2": true, "fat": true}

// compoundOSArch covers tokens that fuse an OS with an implied word size.
var compoundOSArch = map[string]struct{ goos, goarch string }{
	"win64":    {"windows", "amd64"},
	"win32":    {"windows", "386"},
	"linux64":  {"linux", "amd64"},
	"linux32":  {"linux", "386"},
	"macos64":  {"darwin", "amd64"},
	"osx64":    {"darwin", "amd64"},
	"darwin64": {"darwin", "amd64"},
}

// libcAliases maps a token to the C library it denotes. On Windows these same
// tokens name the toolchain ABI instead, which scoring accounts for.
var libcAliases = map[string]string{
	"gnu": "gnu", "glibc": "gnu", "gnueabi": "gnu", "gnueabihf": "gnu",
	"musl": "musl", "musleabi": "musl", "musleabihf": "musl", "alpine": "musl",
	"msvc": "msvc", "mingw": "gnu", "mingw64": "gnu",
}

// neutralTokens carry no platform meaning and should not count against a candidate.
var neutralTokens = map[string]bool{
	"unknown": true, "pc": true, "apple": true, "none": true, "portbld": true,
	"exe": true, "bin": true,
}

// numericToken matches the version fragments left behind after splitting on dots.
var numericToken = regexp.MustCompile(`^v?\d+$`)

// archiveFormats lists recognised archive suffixes, longest first so that
// ".tar.gz" is preferred over ".gz". Extractable records whether ghget can
// unpack the format; the rest may still be downloaded.
var archiveFormats = []struct {
	suffix      string
	tar         bool
	extractable bool
}{
	{".tar.gz", true, true},
	{".tar.bz2", true, false},
	{".tar.xz", true, false},
	{".tar.zst", true, false},
	{".tbz2", true, false},
	{".txz", true, false},
	{".tzst", true, false},
	{".tgz", true, true},
	{".tar", true, true},
	{".zip", false, true},
	{".7z", false, false},
	{".gz", false, true},
	{".bz2", false, false},
	{".xz", false, false},
	{".zst", false, false},
}

// excludedSuffixes name assets that are never the program itself: checksum and
// signature sidecars, attestations, documentation, and OS package formats.
var excludedSuffixes = []string{
	".sha256", ".sha512", ".sha1", ".md5", ".sha256sum", ".sha512sum", ".sum",
	".asc", ".sig", ".minisig", ".pem", ".cert", ".crt", ".pub",
	".json", ".jsonl", ".txt", ".md", ".yaml", ".yml", ".xml", ".html",
	".deb", ".rpm", ".apk", ".msi", ".pkg", ".dmg", ".nupkg", ".snap", ".flatpak",
	".appimage", ".zsync", ".sh", ".ps1", ".bat", ".cmd", ".sbom", ".spdx", ".sblob",
}

// format describes the container an asset is published in.
type format struct {
	suffix      string
	tar         bool
	extractable bool
	archive     bool
}

// splitFormat separates a recognised archive suffix from the rest of the name.
func splitFormat(lower string) (string, format) {
	for _, candidate := range archiveFormats {
		if stem, ok := strings.CutSuffix(lower, candidate.suffix); ok {
			return stem, format{
				suffix:      candidate.suffix,
				tar:         candidate.tar,
				extractable: candidate.extractable,
				archive:     true,
			}
		}
	}
	return lower, format{}
}

// excluded reports whether an asset can be ruled out from its name alone.
func excluded(lower string) (string, bool) {
	for _, suffix := range excludedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return suffix, true
		}
	}
	return "", false
}

// tokenize splits an asset stem into lowercase tokens, rejoining the "x86" and
// "64" halves that separator splitting would otherwise produce from "x86_64".
func tokenize(stem string) []string {
	fields := strings.FieldsFunc(stem, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '+' || r == '~'
	})
	tokens := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		if fields[i] == "x86" && i+1 < len(fields) && fields[i+1] == "64" {
			tokens = append(tokens, "x86_64")
			i++
			continue
		}
		tokens = append(tokens, fields[i])
	}
	return tokens
}

// facts records what an asset name says about the platform it targets.
type facts struct {
	oses         []string
	arches       []string
	universal    bool
	implicitOS   string
	implicitArch string
	libc         string
	unrecognized int
}

// classify interprets each token of an asset stem.
func classify(tokens []string) facts {
	var f facts
	for _, token := range tokens {
		switch {
		case osAliases[token] != "":
			f.oses = appendUnique(f.oses, osAliases[token])
		case archAliases[token] != "":
			f.arches = appendUnique(f.arches, archAliases[token])
		case universalArches[token]:
			f.universal = true
		case libcAliases[token] != "":
			if f.libc == "" {
				f.libc = libcAliases[token]
			}
		case neutralTokens[token], numericToken.MatchString(token):
			// Recognised as carrying no platform meaning.
		default:
			if compound, ok := compoundOSArch[token]; ok {
				f.implicitOS, f.implicitArch = compound.goos, compound.goarch
				continue
			}
			f.unrecognized++
		}
	}
	return f
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
