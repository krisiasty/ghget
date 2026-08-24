package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Options struct {
	Force bool
	Flat  bool
}

type FileResult struct {
	Path    string
	Written bool
}

func Extract(archivePath, destination, assetName string, options Options) ([]FileResult, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZIP(archivePath, destination, options)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGzip(archivePath, destination, options)
	case strings.HasSuffix(lower, ".tar"):
		f, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return extractTar(f, destination, options)
	case strings.HasSuffix(lower, ".gz"):
		return extractGzip(archivePath, destination, strings.TrimSuffix(assetName, filepath.Ext(assetName)), options)
	default:
		return nil, fmt.Errorf("cannot extract %s: supported formats are .zip, .tar, .tar.gz, .tgz, and .gz", assetName)
	}
}

func extractZIP(archivePath, destination string, options Options) ([]FileResult, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open ZIP archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

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
			target, err := targetPath(destination, clean, false)
			if err != nil {
				return results, err
			}
			if err := os.MkdirAll(target, directoryMode(mode)); err != nil {
				return results, err
			}
			continue
		}
		if !mode.IsRegular() {
			return results, fmt.Errorf("unsupported ZIP entry type %q", entry.Name)
		}
		target, err := targetPath(destination, clean, options.Flat)
		if err != nil {
			return results, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return results, err
		}
		r, err := entry.Open()
		if err != nil {
			return results, err
		}
		written, err := writeFile(target, r, fileMode(mode), options.Force)
		if err != nil {
			_ = r.Close()
			return results, err
		}
		if err := r.Close(); err != nil {
			return results, err
		}
		results = append(results, FileResult{Path: target, Written: written})
	}
	return results, nil
}

func extractTarGzip(archivePath, destination string, options Options) ([]FileResult, error) {
	f, err := os.Open(archivePath)
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
	tr := tar.NewReader(r)
	results := make([]FileResult, 0)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return results, fmt.Errorf("read TAR archive: %w", err)
		}
		clean, err := cleanArchivePath(header.Name)
		if err != nil {
			return results, err
		}
		mode := fs.FileMode(header.Mode)
		switch header.Typeflag {
		case tar.TypeDir:
			if options.Flat {
				continue
			}
			target, err := targetPath(destination, clean, false)
			if err != nil {
				return results, err
			}
			if err := os.MkdirAll(target, directoryMode(mode)); err != nil {
				return results, err
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // NUL is still emitted by legacy TAR writers.
			target, err := targetPath(destination, clean, options.Flat)
			if err != nil {
				return results, err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return results, err
			}
			written, err := writeFile(target, io.LimitReader(tr, header.Size), fileMode(mode), options.Force)
			if err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: target, Written: written})
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
	f, err := os.Open(archivePath)
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
	clean, err := cleanArchivePath(outputName)
	if err != nil {
		return nil, err
	}
	target, err := targetPath(destination, clean, options.Flat)
	if err != nil {
		return nil, err
	}
	written, err := writeFile(target, gz, 0o644, options.Force)
	if err != nil {
		return nil, err
	}
	return []FileResult{{Path: target, Written: written}}, nil
}

func cleanArchivePath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return clean, nil
}

func targetPath(destination, clean string, flat bool) (string, error) {
	if flat {
		clean = filepath.Base(clean)
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, clean)
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", clean)
	}
	for candidate := target; candidate != root; candidate = filepath.Dir(candidate) {
		info, err := os.Lstat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("unsafe archive path %q traverses a symbolic link", clean)
		}
	}
	return target, nil
}

func writeFile(path string, r io.Reader, mode fs.FileMode, force bool) (bool, error) {
	if force {
		return true, replaceFile(path, r, mode)
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("destination already exists and is not a regular file: %s; use --force to overwrite", path)
		}
		matches, err := contentMatchesFile(path, r)
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

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return false, err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return false, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
	}
	return true, closeErr
}

func contentMatchesFile(path string, content io.Reader) (bool, error) {
	existing, err := os.Open(path)
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

func replaceFile(path string, r io.Reader, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ghget-extract-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	_, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("cannot overwrite directory %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpPath, path)
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
