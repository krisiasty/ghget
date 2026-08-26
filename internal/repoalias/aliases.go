// Package repoalias resolves built-in tool aliases to GitHub repositories.
package repoalias

import (
	"bufio"
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/klauspost/compress/zstd"
)

const maxRegistrySize = 16 << 20

//go:generate go run ./cmd/generate -input ../../registry/aliases.tsv -output aliases.tsv.zst

//go:embed aliases.tsv.zst
var compressedAliases []byte

// Entry maps a case-insensitive alias to a GitHub OWNER/REPO identifier. An
// optional asset hint identifies the program when a repository publishes
// several independently packaged tools.
type Entry struct {
	Alias      string
	Repository string
	AssetHint  string
}

var loadEmbedded = sync.OnceValues(func() ([]Entry, error) {
	decoder, err := zstd.NewReader(bytes.NewReader(compressedAliases), zstd.WithDecoderMaxMemory(maxRegistrySize))
	if err != nil {
		return nil, fmt.Errorf("open embedded repository aliases: %w", err)
	}
	defer decoder.Close()

	data, err := io.ReadAll(io.LimitReader(decoder, maxRegistrySize+1))
	if err != nil {
		return nil, fmt.Errorf("decompress embedded repository aliases: %w", err)
	}
	if len(data) > maxRegistrySize {
		return nil, fmt.Errorf("embedded repository aliases exceed %d bytes", maxRegistrySize)
	}
	return Parse(bytes.NewReader(data), "embedded repository aliases")
})

// Lookup resolves alias without regard to case.
func Lookup(alias string) (Entry, bool, error) {
	entries, err := loadEmbedded()
	if err != nil {
		return Entry{}, false, err
	}
	wanted := strings.ToLower(alias)
	index := sort.Search(len(entries), func(i int) bool { return entries[i].Alias >= wanted })
	if index == len(entries) || entries[index].Alias != wanted {
		return Entry{}, false, nil
	}
	return entries[index], true, nil
}

// Parse reads aliases, repositories, and optional asset hints separated by any
// non-empty run of whitespace. Blank lines and full-line comments beginning
// with # are ignored.
func Parse(reader io.Reader, source string) ([]Entry, error) {
	entries := make([]Entry, 0)
	seen := make(map[string]int)
	scanner := bufio.NewScanner(reader)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields) > 3 {
			return nil, fmt.Errorf(
				"%s:%d: expected alias, OWNER/REPO, and optional asset hint, found %d fields",
				source,
				lineNumber,
				len(fields),
			)
		}
		alias := strings.ToLower(fields[0])
		repository := fields[1]
		assetHint := ""
		if len(fields) == 3 {
			assetHint = strings.ToLower(fields[2])
		}
		if err := validateAlias(alias); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", source, lineNumber, err)
		}
		if err := validateRepository(repository); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", source, lineNumber, err)
		}
		if assetHint != "" {
			if err := validateAssetHint(assetHint); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", source, lineNumber, err)
			}
		}
		if previous, exists := seen[alias]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate alias %q first declared on line %d", source, lineNumber, alias, previous)
		}
		seen[alias] = lineNumber
		entries = append(entries, Entry{Alias: alias, Repository: repository, AssetHint: assetHint})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Alias < entries[j].Alias })
	return entries, nil
}

func validateAlias(alias string) error {
	if alias == "" {
		return errors.New("alias must not be empty")
	}
	for _, character := range alias {
		if unicode.IsSpace(character) || character == '/' || character == '\\' || character == '@' {
			return fmt.Errorf("invalid alias %q", alias)
		}
	}
	return nil
}

func validateAssetHint(assetHint string) error {
	for _, character := range assetHint {
		if unicode.IsSpace(character) || character == '/' || character == '\\' || character == '@' {
			return fmt.Errorf("invalid asset hint %q", assetHint)
		}
	}
	return nil
}

func validateRepository(repository string) error {
	owner, name, found := strings.Cut(repository, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") ||
		owner == "." || owner == ".." || name == "." || name == ".." || strings.ContainsAny(repository, "\\@") {
		return fmt.Errorf("invalid repository %q; expected OWNER/REPO", repository)
	}
	return nil
}
