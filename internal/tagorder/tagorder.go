// Package tagorder sorts release tags and provides semantic-version variants.
package tagorder

import (
	"sort"
	"strings"
)

type version struct {
	major      string
	minor      string
	patch      string
	prerelease []identifier
}

type identifier struct {
	value   string
	numeric bool
}

type parsedTag struct {
	tag     string
	version version
}

// Sort returns tags in descending semantic-version precedence when every tag
// is SemVer-compatible. If any tag is not SemVer, it returns all tags in
// ascending alphabetic order. A single synthetic "latest" is always first.
func Sort(tags []string) []string {
	filtered := make([]string, 0, len(tags))
	parsed := make([]parsedTag, 0, len(tags))
	allSemVer := true
	for _, tag := range tags {
		if tag == "latest" {
			continue
		}
		filtered = append(filtered, tag)
		v, ok := parse(tag)
		if !ok {
			allSemVer = false
			continue
		}
		parsed = append(parsed, parsedTag{tag: tag, version: v})
	}

	if allSemVer {
		sort.Slice(parsed, func(i, j int) bool {
			comparison := compare(parsed[i].version, parsed[j].version)
			if comparison == 0 {
				return parsed[i].tag < parsed[j].tag
			}
			return comparison > 0
		})
		filtered = filtered[:0]
		for _, item := range parsed {
			filtered = append(filtered, item.tag)
		}
	} else {
		sort.Strings(filtered)
	}

	return append([]string{"latest"}, filtered...)
}

// Variants returns the original tag followed by its alternate leading-v
// spelling when tag is a semantic version.
func Variants(tag string) []string {
	if _, ok := parse(tag); !ok {
		return []string{tag}
	}
	withoutV := strings.TrimPrefix(tag, "v")
	if tag == withoutV {
		return []string{tag, "v" + tag}
	}
	return []string{tag, withoutV}
}

// IsNewer reports whether candidate has higher semantic-version precedence
// than current. Invalid versions cannot establish that an update is available.
func IsNewer(candidate, current string) bool {
	candidateVersion, candidateOK := parse(candidate)
	currentVersion, currentOK := parse(current)
	return candidateOK && currentOK && compare(candidateVersion, currentVersion) > 0
}

func parse(tag string) (version, bool) {
	value := strings.TrimPrefix(tag, "v")
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return version{}, false
	}

	withoutBuild, build, hasBuild := strings.Cut(value, "+")
	if hasBuild && !validIdentifiers(build, false) {
		return version{}, false
	}
	core, prerelease, hasPrerelease := strings.Cut(withoutBuild, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 || !validCoreNumber(parts[0]) || !validCoreNumber(parts[1]) || !validCoreNumber(parts[2]) {
		return version{}, false
	}

	result := version{major: parts[0], minor: parts[1], patch: parts[2]}
	if hasPrerelease {
		if !validIdentifiers(prerelease, true) {
			return version{}, false
		}
		for _, part := range strings.Split(prerelease, ".") {
			result.prerelease = append(result.prerelease, identifier{value: part, numeric: isNumeric(part)})
		}
	}
	return result, true
}

func validCoreNumber(value string) bool {
	return isNumeric(value) && (value == "0" || value[0] != '0')
}

func validIdentifiers(value string, prerelease bool) bool {
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for _, char := range part {
			if !isIdentifierChar(char) {
				return false
			}
		}
		if prerelease && isNumeric(part) && len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

func isIdentifierChar(char rune) bool {
	return char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char == '-'
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func compare(a, b version) int {
	if result := compareNumber(a.major, b.major); result != 0 {
		return result
	}
	if result := compareNumber(a.minor, b.minor); result != 0 {
		return result
	}
	if result := compareNumber(a.patch, b.patch); result != 0 {
		return result
	}
	if len(a.prerelease) == 0 && len(b.prerelease) == 0 {
		return 0
	}
	if len(a.prerelease) == 0 {
		return 1
	}
	if len(b.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		left, right := a.prerelease[i], b.prerelease[i]
		switch {
		case left.numeric && right.numeric:
			if result := compareNumber(left.value, right.value); result != 0 {
				return result
			}
		case left.numeric:
			return -1
		case right.numeric:
			return 1
		default:
			if result := strings.Compare(left.value, right.value); result != 0 {
				return result
			}
		}
	}
	return compareInt(len(a.prerelease), len(b.prerelease))
}

func compareNumber(a, b string) int {
	if len(a) != len(b) {
		return compareInt(len(a), len(b))
	}
	return strings.Compare(a, b)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
