package checksum

import (
	"bufio"
	"crypto/md5"  // #nosec G501 -- MD5 is supported for release-file integrity compatibility.
	"crypto/sha1" // #nosec G505 -- SHA-1 is supported for release-file integrity compatibility.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	digestRE        = regexp.MustCompile(`(?i)\b([a-f0-9]{32}|[a-f0-9]{40}|[a-f0-9]{64}|[a-f0-9]{128})\b`)
	bsdRE           = regexp.MustCompile(`(?i)^\s*[a-z0-9-]+\s*\((.+)\)\s*=\s*([a-f0-9]+)\s*$`)
	sidecarSuffixes = []string{".md5", ".sha1", ".sha256", ".sha512", ".md5sum", ".sha1sum", ".sha256sum", ".sha512sum", ".checksum"}
)

type Entry struct {
	Filename string
	Digest   string
}

type MismatchError struct {
	AssetName string
	Expected  string
	Actual    string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("checksum mismatch for %s: expected %s, got %s", e.AssetName, e.Expected, e.Actual)
}

func Parse(r io.Reader) ([]Entry, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	entries := make([]Entry, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if match := bsdRE.FindStringSubmatch(line); match != nil && supportedDigest(match[2]) {
			entries = append(entries, Entry{Filename: strings.TrimSpace(match[1]), Digest: strings.ToLower(match[2])})
			continue
		}
		loc := digestRE.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		digest := strings.ToLower(line[loc[2]:loc[3]])
		before := strings.TrimSpace(line[:loc[0]])
		after := strings.TrimSpace(line[loc[1]:])
		filename := ""
		switch {
		case after != "":
			filename = strings.TrimSpace(strings.TrimPrefix(after, "*"))
		case before != "":
			filename = strings.TrimSpace(strings.TrimSuffix(before, ":"))
		}
		entries = append(entries, Entry{Filename: filename, Digest: digest})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksum file: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no supported checksums found")
	}
	return entries, nil
}

func ParseValueOrFile(value string) ([]Entry, error) {
	if supportedDigest(value) {
		return []Entry{{Digest: strings.ToLower(value)}}, nil
	}
	f, err := os.Open(value)
	if err != nil {
		return nil, fmt.Errorf("checksum must be a supported digest or readable file: %w", err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

func VerifyFile(path, assetName string, entries []Entry) error {
	entry, ok := findEntry(assetName, entries)
	if !ok {
		return fmt.Errorf("no checksum found for %s", assetName)
	}
	h, err := hasherFor(entry.Digest)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("hash %s: %w", assetName, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != strings.ToLower(entry.Digest) {
		return &MismatchError{AssetName: assetName, Expected: entry.Digest, Actual: actual}
	}
	return nil
}

func HasEntry(assetName string, entries []Entry) bool {
	_, ok := findEntry(assetName, entries)
	return ok
}

func IsChecksumAsset(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".sig", ".asc", ".minisig", ".pem"} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	for _, suffix := range sidecarSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	base := strings.TrimSuffix(lower, filepath.Ext(lower))
	return strings.Contains(lower, "checksum") || strings.Contains(lower, "checksums") ||
		strings.Contains(lower, "sha256sum") || strings.Contains(lower, "sha512sum") ||
		strings.Contains(lower, "sha1sum") || strings.Contains(lower, "md5sum") ||
		base == "sha256sums" || base == "sha512sums" || base == "sha1sums" || base == "md5sums"
}

func TargetFromSidecar(name string) string {
	lower := strings.ToLower(name)
	for _, suffix := range sidecarSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return ""
}

func findEntry(assetName string, entries []Entry) (Entry, bool) {
	var unnamed *Entry
	for i := range entries {
		name := strings.TrimPrefix(strings.TrimSpace(entries[i].Filename), "./")
		if name == assetName || filepath.Base(name) == assetName {
			return entries[i], true
		}
		if name == "" && unnamed == nil {
			unnamed = &entries[i]
		}
	}
	if unnamed != nil && len(entries) == 1 {
		return *unnamed, true
	}
	return Entry{}, false
}

func supportedDigest(value string) bool {
	if value != strings.TrimSpace(value) {
		return false
	}
	length := len(value)
	if length != md5.Size*2 && length != sha1.Size*2 && length != sha256.Size*2 && length != sha512.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hasherFor(digest string) (hash.Hash, error) {
	switch len(digest) {
	case md5.Size * 2:
		return md5.New(), nil // #nosec G401 -- compatibility with published release checksums.
	case sha1.Size * 2:
		return sha1.New(), nil // #nosec G401 -- compatibility with published release checksums.
	case sha256.Size * 2:
		return sha256.New(), nil
	case sha512.Size * 2:
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum length %d", len(digest))
	}
}
