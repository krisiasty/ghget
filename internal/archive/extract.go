// Package archive safely extracts release archives into local directories.
package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options controls replacement and path handling during extraction.
type Options struct {
	Force bool
	Flat  bool
}

// FileResult describes whether an extracted path was written or already matched.
type FileResult struct {
	Path    string
	Written bool
}

// Extract unpacks a supported archive while rejecting unsafe paths and links.
func Extract(archivePath, destination, assetName string, options Options) ([]FileResult, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZIP(archivePath, destination, options)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGzip(archivePath, destination, options)
	case strings.HasSuffix(lower, ".tar.zst"), strings.HasSuffix(lower, ".tzst"):
		return extractTarZstd(archivePath, destination, options)
	case strings.HasSuffix(lower, ".tar"):
		f, err := os.Open(archivePath) //nolint:gosec // archivePath is the verified temporary release asset selected by the caller.
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return extractTar(f, destination, options)
	case strings.HasSuffix(lower, ".gz"):
		return extractGzip(archivePath, destination, strings.TrimSuffix(assetName, filepath.Ext(assetName)), options)
	case strings.HasSuffix(lower, ".zst"):
		return extractZstd(archivePath, destination, strings.TrimSuffix(assetName, filepath.Ext(assetName)), options)
	default:
		return nil, fmt.Errorf("cannot extract %s: supported formats are %s", assetName, strings.Join(supportedSuffixes, ", "))
	}
}

func extractZIP(archivePath, destination string, options Options) ([]FileResult, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open ZIP archive: %w", err)
	}
	defer func() { _ = zr.Close() }()
	root, rootPath, err := openDestinationRoot(destination)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	results := make([]FileResult, 0)
	for _, entry := range zr.File {
		clean, err := cleanArchivePath(entry.Name)
		if err != nil {
			return results, err
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			return results, fmt.Errorf("refusing unsafe ZIP symbolic link %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if options.Flat {
				continue
			}
			if err := rejectSymlinkComponents(root, clean); err != nil {
				return results, err
			}
			if err := root.MkdirAll(clean, directoryMode(mode)); err != nil {
				return results, err
			}
			continue
		}
		if !mode.IsRegular() {
			return results, fmt.Errorf("unsupported ZIP entry type %q", entry.Name)
		}
		target := archiveTarget(clean, options.Flat)
		if err := rejectSymlinkComponents(root, target); err != nil {
			return results, err
		}
		if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return results, err
		}
		r, err := entry.Open()
		if err != nil {
			return results, err
		}
		written, err := writeFile(root, target, r, fileMode(mode), options.Force)
		if err != nil {
			_ = r.Close()
			return results, err
		}
		if err := r.Close(); err != nil {
			return results, err
		}
		results = append(results, FileResult{Path: filepath.Join(rootPath, target), Written: written})
	}
	return results, nil
}

func extractTarGzip(archivePath, destination string, options Options) ([]FileResult, error) {
	f, err := os.Open(archivePath) //nolint:gosec // archivePath is the verified temporary release asset selected by the caller.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()
	return extractTar(gz, destination, options)
}

func extractTar(r io.Reader, destination string, options Options) ([]FileResult, error) {
	root, rootPath, err := openDestinationRoot(destination)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	tr := tar.NewReader(r)
	results := make([]FileResult, 0)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return results, fmt.Errorf("read TAR archive: %w", err)
		}
		clean, err := cleanArchivePath(header.Name)
		if err != nil {
			return results, err
		}
		mode := fs.FileMode(header.Mode & 0o777)
		switch header.Typeflag {
		case tar.TypeDir:
			if options.Flat {
				continue
			}
			if err := rejectSymlinkComponents(root, clean); err != nil {
				return results, err
			}
			if err := root.MkdirAll(clean, directoryMode(mode)); err != nil {
				return results, err
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // NUL is still emitted by legacy TAR writers.
			target := archiveTarget(clean, options.Flat)
			if err := rejectSymlinkComponents(root, target); err != nil {
				return results, err
			}
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return results, err
			}
			written, err := writeFile(root, target, io.LimitReader(tr, header.Size), fileMode(mode), options.Force)
			if err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: filepath.Join(rootPath, target), Written: written})
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeSymlink, tar.TypeLink:
			return results, fmt.Errorf("refusing unsafe TAR link %q", header.Name)
		default:
			return results, fmt.Errorf("unsupported TAR entry type %q", header.Name)
		}
	}
	return results, nil
}

func extractGzip(archivePath, destination, outputName string, options Options) ([]FileResult, error) {
	f, err := os.Open(archivePath) //nolint:gosec // archivePath is the verified temporary release asset selected by the caller.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()
	if gz.Name != "" && filepath.Base(gz.Name) == gz.Name {
		outputName = gz.Name
	}
	return extractStream(gz, destination, outputName, options)
}

// extractStream writes one decompressed stream to destination under outputName.
func extractStream(r io.Reader, destination, outputName string, options Options) ([]FileResult, error) {
	clean, err := cleanArchivePath(outputName)
	if err != nil {
		return nil, err
	}
	root, rootPath, err := openDestinationRoot(destination)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	target := archiveTarget(clean, options.Flat)
	if err := rejectSymlinkComponents(root, target); err != nil {
		return nil, err
	}
	if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	written, err := writeFile(root, target, r, 0o644, options.Force)
	if err != nil {
		return nil, err
	}
	return []FileResult{{Path: filepath.Join(rootPath, target), Written: written}}, nil
}

func cleanArchivePath(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return clean, nil
}

func archiveTarget(clean string, flat bool) string {
	if flat {
		return filepath.Base(clean)
	}
	return clean
}

func openDestinationRoot(destination string) (*os.Root, string, error) {
	root, err := filepath.Abs(destination)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil { //nolint:gosec // User-selected extraction directories should use conventional directory permissions.
		return nil, "", err
	}
	destinationRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	return destinationRoot, root, nil
}

func rejectSymlinkComponents(root *os.Root, name string) error {
	for candidate := name; candidate != "."; candidate = filepath.Dir(candidate) {
		info, err := root.Lstat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe archive path %q traverses a symbolic link", name)
		}
	}
	return nil
}

func writeFile(root *os.Root, path string, r io.Reader, mode fs.FileMode, force bool) (bool, error) {
	if force {
		return true, replaceFile(root, path, r, mode)
	}
	info, err := root.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("destination already exists and is not a regular file: %s; use --force to overwrite", path)
		}
		matches, err := contentMatchesFile(root, path, r)
		if err != nil {
			return false, fmt.Errorf("compare existing file %s: %w", path, err)
		}
		if matches {
			return false, nil
		}
		return false, fmt.Errorf("existing file differs: %s; use --force to overwrite", path)
	}
	if !os.IsNotExist(err) {
		return false, err
	}

	f, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return false, err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = root.Remove(path)
		return false, copyErr
	}
	if closeErr != nil {
		_ = root.Remove(path)
	}
	return true, closeErr
}

func contentMatchesFile(root *os.Root, path string, content io.Reader) (bool, error) {
	existing, err := root.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = existing.Close() }()

	contentBuffer := make([]byte, 32*1024)
	existingBuffer := make([]byte, len(contentBuffer))
	for {
		n, readErr := content.Read(contentBuffer)
		if n > 0 {
			existingN, existingErr := io.ReadFull(existing, existingBuffer[:n])
			if existingErr == io.EOF || existingErr == io.ErrUnexpectedEOF {
				return false, nil
			}
			if existingErr != nil {
				return false, existingErr
			}
			if existingN != n || !bytes.Equal(contentBuffer[:n], existingBuffer[:n]) {
				return false, nil
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return false, readErr
			}
			extraN, extraErr := existing.Read(existingBuffer[:1])
			if extraN > 0 {
				return false, nil
			}
			if extraErr != nil && extraErr != io.EOF {
				return false, extraErr
			}
			return true, nil
		}
	}
}

func replaceFile(root *os.Root, path string, r io.Reader, mode fs.FileMode) error {
	tmp, tmpPath, err := createTempFile(root, filepath.Dir(path), mode)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmpPath) }()
	_, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if info, err := root.Lstat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("cannot overwrite directory %s", path)
		}
		if err := root.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return root.Rename(tmpPath, path)
}

func createTempFile(root *os.Root, directory string, mode fs.FileMode) (*os.File, string, error) {
	for range 100 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, "", err
		}
		path := filepath.Join(directory, ".ghget-extract-"+hex.EncodeToString(suffix[:]))
		f, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
		if err == nil {
			if err := f.Chmod(mode.Perm()); err != nil {
				_ = f.Close()
				_ = root.Remove(path)
				return nil, "", err
			}
			return f, path, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("could not create a unique extraction temporary file")
}

func directoryMode(mode fs.FileMode) fs.FileMode {
	if mode.Perm() == 0 {
		return 0o755
	}
	return mode.Perm()
}

func fileMode(mode fs.FileMode) fs.FileMode {
	if mode.Perm() == 0 {
		return 0o644
	}
	return mode.Perm()
}
