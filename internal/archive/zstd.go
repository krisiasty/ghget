package archive

import (
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// maxZstdWindow bounds the decompression window a zstd stream may ask for.
// Release archives need a few megabytes at most; the library would otherwise
// honour a window of up to 64 GiB declared by a hostile frame.
const maxZstdWindow = 1 << 30

// extractTarZstd unpacks a zstd-compressed TAR archive.
func extractTarZstd(archivePath, destination string, options Options) ([]FileResult, error) {
	return extractTarArchive(func() (io.ReadCloser, error) {
		f, err := os.Open(archivePath) //nolint:gosec // archivePath is the verified temporary release asset selected by the caller.
		if err != nil {
			return nil, err
		}
		decoder, err := zstd.NewReader(f, zstd.WithDecoderMaxMemory(maxZstdWindow))
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("open zstd stream: %w", err)
		}
		return &readerCloser{Reader: decoder, close: func() error {
			decoder.Close()
			return f.Close()
		}}, nil
	}, destination, options)
}

// extractZstd unpacks a single zstd-compressed file under outputName.
func extractZstd(archivePath, destination, outputName string, options Options) ([]FileResult, error) {
	f, err := os.Open(archivePath) //nolint:gosec // archivePath is the verified temporary release asset selected by the caller.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	decoder, err := zstd.NewReader(f, zstd.WithDecoderMaxMemory(maxZstdWindow))
	if err != nil {
		return nil, fmt.Errorf("open zstd stream: %w", err)
	}
	defer decoder.Close()
	return extractStream(decoder, destination, outputName, options)
}
