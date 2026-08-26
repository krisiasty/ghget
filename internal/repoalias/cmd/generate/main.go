// Command generate builds ghget's compressed repository-alias registry.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/krisiasty/ghget/internal/repoalias"
)

func main() {
	input := flag.String("input", "", "source alias registry")
	output := flag.String("output", "", "compressed output path")
	flag.Parse()
	if err := generate(*input, *output); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(inputPath, outputPath string) error {
	if inputPath == "" || outputPath == "" {
		return errors.New("both -input and -output are required")
	}
	input, err := os.Open(inputPath) //nolint:gosec // The path is supplied by the repository-owned go:generate directive.
	if err != nil {
		return err
	}
	entries, parseErr := repoalias.Parse(input, inputPath)
	closeErr := input.Close()
	if parseErr != nil {
		return parseErr
	}
	if closeErr != nil {
		return closeErr
	}

	var normalized strings.Builder
	for _, entry := range entries {
		normalized.WriteString(entry.Alias)
		normalized.WriteByte('\t')
		normalized.WriteString(entry.Repository)
		if entry.AssetHint != "" {
			normalized.WriteByte('\t')
			normalized.WriteString(entry.AssetHint)
		}
		normalized.WriteByte('\n')
	}
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return err
	}
	if _, err := encoder.Write([]byte(normalized.String())); err != nil {
		_ = encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, compressed.Bytes(), 0o644); err != nil { //nolint:gosec // The generated registry is public build input.
		return err
	}
	return nil
}
