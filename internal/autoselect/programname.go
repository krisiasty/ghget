package autoselect

import "strings"

// programExtensions are kept on the installed name so a Windows program stays runnable.
var programExtensions = []string{".exe", ".com", ".bat", ".cmd"}

// ProgramName reduces a bare-executable asset name to the program it contains,
// by dropping the platform and version tokens that only describe the build.
// For example "kind-linux-amd64" becomes "kind".
//
// The original name is returned when nothing recognisable remains, because a
// name made entirely of platform tokens says nothing about the program.
func ProgramName(asset string) string {
	stem, extension := asset, ""
	for _, candidate := range programExtensions {
		if trimmed, ok := cutSuffixFold(asset, candidate); ok {
			stem, extension = trimmed, asset[len(trimmed):]
			break
		}
	}
	kept := make([]string, 0, 4)
	for _, token := range tokenize(stem) {
		if isPlatformToken(strings.ToLower(token)) {
			continue
		}
		kept = append(kept, token)
	}
	if len(kept) == 0 {
		return asset
	}
	return strings.Join(kept, "-") + extension
}

// isPlatformToken reports whether a token describes the build rather than the program.
func isPlatformToken(token string) bool {
	if _, ok := compoundOSArch[token]; ok {
		return true
	}
	return osAliases[token] != "" || archAliases[token] != "" || libcAliases[token] != "" ||
		universalArches[token] || neutralTokens[token] || numericToken.MatchString(token)
}

func cutSuffixFold(value, suffix string) (string, bool) {
	if len(value) < len(suffix) || !strings.EqualFold(value[len(value)-len(suffix):], suffix) {
		return value, false
	}
	return value[:len(value)-len(suffix)], true
}
