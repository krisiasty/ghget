package archive

import "strings"

// supportedSuffixes are the archive formats Extract can unpack.
var supportedSuffixes = []string{".zip", ".tar.gz", ".tgz", ".tar", ".gz"}

// Supported reports whether an asset name names an archive Extract can unpack.
func Supported(assetName string) bool {
	lower := strings.ToLower(assetName)
	for _, suffix := range supportedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}
