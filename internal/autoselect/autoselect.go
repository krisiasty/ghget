// Package autoselect picks the release asset built for a given platform,
// without requiring the caller to know the project's naming convention.
//
// Selection works in two stages. Assets that name a different operating
// system, architecture, or an unusable C library are rejected outright, so a
// wrong-platform download is not merely unlikely but impossible. Whatever
// survives is then ranked by preference, and a tie at the top is reported as
// ambiguous rather than guessed.
package autoselect

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/krisiasty/ghget/internal/platform"
)

// ErrNoMatch reports that no published asset targets the requested platform.
var ErrNoMatch = errors.New("no release asset matches this platform")

// AmbiguousError reports that several assets are equally good candidates.
type AmbiguousError struct {
	Names []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d release assets match this platform equally well: %s", len(e.Names), strings.Join(e.Names, ", "))
}

// Candidate is one asset considered during selection.
type Candidate struct {
	// Name is the published asset name.
	Name string
	// Reason summarises why the asset was kept or rejected.
	Reason string
	// Archive reports whether the asset is a recognised archive format.
	Archive bool
	// Extractable reports whether ghget can unpack the archive.
	Extractable bool

	rank []int
}

// Result reports the outcome of selection, including the assets that lost.
type Result struct {
	// Selected is the winning asset name, empty when selection failed.
	Selected string
	// Viable holds the surviving candidates, best first.
	Viable []Candidate
	// Rejected holds the eliminated candidates, each with a reason.
	Rejected []Candidate
}

// Candidate returns the entry for name, if selection considered it viable.
func (r Result) Candidate(name string) (Candidate, bool) {
	for _, candidate := range r.Viable {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return Candidate{}, false
}

// Select chooses the asset built for target from names. The project name is
// the repository name, used to tell a release's own program apart from the
// helpers published beside it.
//
// It returns ErrNoMatch when nothing targets the platform, and *AmbiguousError
// when the best candidates are indistinguishable. Result is populated in every
// case so callers can explain the outcome or override it.
func Select(names []string, target platform.Platform, project string) (Result, error) {
	labelled := releaseLabelsOperatingSystems(names)
	var result Result
	for _, name := range names {
		candidate, ok := evaluate(name, target, project, labelled)
		if !ok {
			result.Rejected = append(result.Rejected, candidate)
			continue
		}
		result.Viable = append(result.Viable, candidate)
	}
	slices.SortStableFunc(result.Viable, func(a, b Candidate) int {
		return slices.Compare(b.rank, a.rank)
	})
	if len(result.Viable) == 0 {
		return result, ErrNoMatch
	}
	tied := []string{result.Viable[0].Name}
	for _, candidate := range result.Viable[1:] {
		if slices.Compare(candidate.rank, result.Viable[0].rank) != 0 {
			break
		}
		tied = append(tied, candidate.Name)
	}
	if len(tied) > 1 {
		return result, &AmbiguousError{Names: tied}
	}
	result.Selected = result.Viable[0].Name
	return result, nil
}

// evaluate decides whether one asset can run on target, and how well it fits.
// releaseLabelsOperatingSystems reports whether any asset in a release names
// the platform it was built for. When one does, an asset that names only an
// architecture is something else — debug symbols, a support bundle, an
// application package — rather than a build for the unnamed platforms.
func releaseLabelsOperatingSystems(names []string) bool {
	for _, name := range names {
		lower := strings.ToLower(name)
		stem, kind := splitFormat(lower)
		if !kind.archive && strings.HasSuffix(lower, windowsProgramSuffix) {
			return true
		}
		if strings.HasSuffix(stem, macOSBundleSuffix) {
			return true
		}
		if f := classify(tokenize(stem), ""); len(f.oses) > 0 || f.implicitOS != "" {
			return true
		}
	}
	return false
}

func evaluate(name string, target platform.Platform, project string, labelled bool) (Candidate, bool) {
	lower := strings.ToLower(name)
	if suffix, ok := excluded(lower); ok {
		return Candidate{Name: name, Reason: "not a program: " + suffix + " file"}, false
	}
	stem, kind := splitFormat(lower)
	candidate := Candidate{Name: name, Archive: kind.archive, Extractable: !kind.archive || kind.extractable}
	windowsProgram := !kind.archive && strings.HasSuffix(lower, windowsProgramSuffix)
	f := classify(tokenize(stem), project)

	if f.debugSymbols {
		candidate.Reason = "not a program: debugging symbols"
		return candidate, false
	}

	oses, arches := f.oses, f.arches
	if len(oses) == 0 && f.implicitOS != "" {
		oses = []string{f.implicitOS}
	}
	if len(oses) == 0 && windowsProgram {
		oses = []string{"windows"}
	}
	if len(oses) == 0 && strings.HasSuffix(stem, macOSBundleSuffix) {
		oses = []string{"darwin"}
	}
	if len(arches) == 0 && f.implicitArch != "" {
		arches = []string{f.implicitArch}
	}
	// Every platform an asset names must match. A name mentioning both "linux"
	// and "android" is an Android build, not a Linux one.
	if foreign, ok := mismatch(oses, target.OS); ok {
		candidate.Reason = "built for " + foreign
		return candidate, false
	}
	if foreign, ok := mismatch(arches, target.Arch); ok && !f.universal {
		candidate.Reason = "built for " + foreign
		return candidate, false
	}
	if len(oses) == 0 && len(arches) == 0 && !f.universal {
		candidate.Reason = "names no operating system or architecture"
		return candidate, false
	}
	if len(oses) == 0 && labelled {
		candidate.Reason = "names no operating system, but this release labels them"
		return candidate, false
	}

	libcRank, ok := rankLibc(f.libc, target)
	if !ok {
		candidate.Reason = "needs " + f.libc + ", but this host uses " + target.Libc.String()
		return candidate, false
	}

	candidate.Reason = describe(oses, arches, f, kind)
	// Publication order is deliberately absent: it would break ties that the
	// caller must be told about. A stable sort preserves it for ranking only.
	candidate.rank = []int{
		libcRank,
		boolRank(len(oses) > 0),
		rankArch(arches, f, target),
		boolRank(f.projectMatch),
		-f.unrecognized,
		rankKind(kind),
		rankFormat(kind, target),
		boolRank(slices.Contains(f.arches, target.Arch) && namesArchExactly(stem, target.Arch)),
	}
	return candidate, true
}

// mismatch reports the first platform an asset names that the target is not.
func mismatch(named []string, target string) (string, bool) {
	for _, value := range named {
		if value != target {
			return strings.Join(named, "/"), true
		}
	}
	return "", false
}

// namesArchExactly reports whether the architecture is spelled the canonical
// way, which separates "linux-amd64" from the "Linux-64bit" copy of the same
// build that some projects publish alongside it.
func namesArchExactly(stem, goarch string) bool {
	return slices.Contains(tokenize(stem), goarch)
}

func boolRank(value bool) int {
	if value {
		return 1
	}
	return 0
}

// rankLibc scores the C library a Linux asset was built against, or the
// toolchain ABI on Windows. A glibc build cannot run on a musl host, so that
// combination is rejected; the reverse merely loses to a glibc build.
func rankLibc(libc string, target platform.Platform) (int, bool) {
	if target.OS == "windows" {
		if libc == "msvc" || libc == "" {
			return 2, true
		}
		return 1, true
	}
	switch {
	case libc == "msvc":
		return 0, false
	case target.Libc == platform.Musl:
		switch libc {
		case "musl":
			return 2, true
		case "gnu":
			return 0, false
		default:
			return 1, true
		}
	case libc == "musl":
		return 1, true
	default:
		return 2, true
	}
}

// rankArch prefers an explicitly named architecture over a universal build,
// and both over a word size merely implied by a token such as "win64".
func rankArch(arches []string, f facts, target platform.Platform) int {
	switch {
	case slices.Contains(f.arches, target.Arch):
		return 3
	case f.universal:
		return 2
	case slices.Contains(arches, target.Arch):
		return 1
	default:
		return 0
	}
}

// rankKind prefers a bare executable over an archive that has to be unpacked.
func rankKind(kind format) int {
	if kind.archive {
		return 1
	}
	return 2
}

// rankFormat prefers the archive format conventional for the platform, and any
// format ghget can extract over one it cannot.
func rankFormat(kind format, target platform.Platform) int {
	if !kind.archive {
		return 3
	}
	if !kind.extractable {
		return 1
	}
	if (target.OS == "windows") == (kind.suffix == ".zip") {
		return 3
	}
	return 2
}

// describe summarises what a viable candidate was understood to be.
func describe(oses, arches []string, f facts, kind format) string {
	parts := make([]string, 0, 4)
	if len(oses) > 0 {
		parts = append(parts, strings.Join(oses, "/"))
	}
	switch {
	case len(arches) > 0:
		parts = append(parts, strings.Join(arches, "/"))
	case f.universal:
		parts = append(parts, "universal")
	}
	if f.libc != "" {
		parts = append(parts, f.libc)
	}
	if kind.archive {
		parts = append(parts, strings.TrimPrefix(kind.suffix, ".")+" archive")
	} else {
		parts = append(parts, "executable")
	}
	if f.unrecognized > 0 {
		parts = append(parts, fmt.Sprintf("%d unrecognised token(s)", f.unrecognized))
	}
	return strings.Join(parts, ", ")
}
