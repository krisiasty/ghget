package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// executableMode makes an installed program runnable by its owner and readable
// by everyone else. It is applied unconditionally, because a program taken from
// a ZIP built with native Windows tooling carries no recorded permissions.
const executableMode = 0o755

// Place copies programs into destination under their own base names and
// returns the paths written. An existing file with identical content is left
// alone; one that differs is an error unless force is set.
func Place(programs []string, destination string, force bool) ([]string, error) {
	// Conventional download directory permissions.
	if err := os.MkdirAll(destination, 0o755); err != nil { //nolint:gosec // Destination directories intentionally follow the convention used elsewhere in ghget.
		return nil, fmt.Errorf("create destination directory: %w", err)
	}
	placed := make([]string, 0, len(programs))
	for _, program := range programs {
		target := filepath.Join(destination, filepath.Base(program))
		if err := place(program, target, force); err != nil {
			return placed, err
		}
		placed = append(placed, target)
	}
	return placed, nil
}

func place(source, target string, force bool) error {
	if !force {
		switch same, err := matchesExistingFile(source, target); {
		case err != nil:
			return err
		case same:
			return nil
		}
	}
	in, err := os.Open(source) //nolint:gosec // source was extracted by ghget into its own temporary directory.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	// Replace rather than truncate, so that overwriting a running program does
	// not corrupt the copy already mapped into memory.
	temporary, err := os.CreateTemp(filepath.Dir(target), ".ghget-install-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := io.Copy(temporary, in); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporary.Name(), executableMode); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), target)
}

// matchesExistingFile reports whether target already holds the same program.
// A missing target is reported as a mismatch so that the copy proceeds.
func matchesExistingFile(source, target string) (bool, error) {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("destination already exists and is not a regular file: %s; use --force to overwrite", target)
	}
	same, err := sameContent(source, target)
	if err != nil {
		return false, fmt.Errorf("compare existing file %s: %w", target, err)
	}
	if !same {
		return false, fmt.Errorf("existing file differs: %s; use --force to overwrite", target)
	}
	return true, nil
}

func sameContent(source, target string) (bool, error) {
	left, err := os.Open(source) //nolint:gosec // source was extracted by ghget into its own temporary directory.
	if err != nil {
		return false, err
	}
	defer func() { _ = left.Close() }()
	right, err := os.Open(target) //nolint:gosec // target is the destination the user selected.
	if err != nil {
		return false, err
	}
	defer func() { _ = right.Close() }()

	leftBuffer := make([]byte, 32*1024)
	rightBuffer := make([]byte, len(leftBuffer))
	for {
		leftN, leftErr := io.ReadFull(left, leftBuffer)
		rightN, rightErr := io.ReadFull(right, rightBuffer)
		if leftN != rightN || !bytes.Equal(leftBuffer[:leftN], rightBuffer[:rightN]) {
			return false, nil
		}
		if leftErr != nil || rightErr != nil {
			return isEOF(leftErr) && isEOF(rightErr), nil
		}
	}
}

func isEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
