package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Extract(archivePath, destination, assetName string) ([]string, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZIP(archivePath, destination)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGzip(archivePath, destination)
	case strings.HasSuffix(lower, ".tar"):
		f, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return extractTar(f, destination)
	case strings.HasSuffix(lower, ".gz"):
		return extractGzip(archivePath, destination, strings.TrimSuffix(assetName, filepath.Ext(assetName)))
	default:
		return nil, fmt.Errorf("cannot extract %s: supported formats are .zip, .tar, .tar.gz, .tgz, and .gz", assetName)
	}
}

func extractZIP(archivePath, destination string) ([]string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open ZIP archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	created := make([]string, 0)
	for _, entry := range zr.File {
		target, err := safeTarget(destination, entry.Name)
		if err != nil {
			return nil, err
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing unsafe ZIP symbolic link %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, directoryMode(mode)); err != nil {
				return nil, err
			}
			continue
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf("unsupported ZIP entry type %q", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		r, err := entry.Open()
		if err != nil {
			return nil, err
		}
		if err := writeFile(target, r, fileMode(mode)); err != nil {
			_ = r.Close()
			return nil, err
		}
		if err := r.Close(); err != nil {
			return nil, err
		}
		created = append(created, target)
	}
	return created, nil
}

func extractTarGzip(archivePath, destination string) ([]string, error) {
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
	return extractTar(gz, destination)
}

func extractTar(r io.Reader, destination string) ([]string, error) {
	tr := tar.NewReader(r)
	created := make([]string, 0)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read TAR archive: %w", err)
		}
		target, err := safeTarget(destination, header.Name)
		if err != nil {
			return nil, err
		}
		mode := fs.FileMode(header.Mode)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, directoryMode(mode)); err != nil {
				return nil, err
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // NUL is still emitted by legacy TAR writers.
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			if err := writeFile(target, io.LimitReader(tr, header.Size), fileMode(mode)); err != nil {
				return nil, err
			}
			created = append(created, target)
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("refusing unsafe TAR link %q", header.Name)
		default:
			return nil, fmt.Errorf("unsupported TAR entry type %q", header.Name)
		}
	}
	return created, nil
}

func extractGzip(archivePath, destination, outputName string) ([]string, error) {
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
	target, err := safeTarget(destination, outputName)
	if err != nil {
		return nil, err
	}
	if err := writeFile(target, gz, 0o644); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

func safeTarget(destination, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, clean)
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
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
			return "", fmt.Errorf("unsafe archive path %q traverses a symbolic link", name)
		}
	}
	return target, nil
}

func writeFile(path string, r io.Reader, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
	}
	return closeErr
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
