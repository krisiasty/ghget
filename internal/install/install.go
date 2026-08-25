// Package install picks the programs out of an unpacked release archive.
//
// Release archives are laid out inconsistently: a bare binary, a binary beside
// its documentation, a wrapper directory, or a whole FHS prefix. Rather than
// model each shape, this package looks for the files that are actually
// programs and ignores everything else.
package install

import (
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ErrNoExecutables reports that an archive holds no program to install.
var ErrNoExecutables = errors.New("archive contains no executable file")

// windowsExtensions are the suffixes Windows treats as directly runnable.
var windowsExtensions = []string{".exe", ".com", ".bat", ".cmd"}

// sharedLibrary matches library filenames, which carry the executable bit on
// many distributions without being programs.
var sharedLibrary = regexp.MustCompile(`\.(so(\.\d+)*|dylib|dll|a)$`)

// Executables returns the programs inside root, sorted by path.
//
// Detection follows the target platform rather than the archive: Windows
// programs are recognised by extension, because ZIPs built with native Windows
// tooling record no Unix mode, while every other platform uses the executable
// bit. When the archive contains a bin directory, only its contents are
// considered; otherwise the shallowest programs win, so that helper tools
// buried deeper in a prefix are left alone.
func Executables(root, goos string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !isProgram(entry.Name(), info.Mode(), goos) {
			return nil
		}
		found = append(found, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, ErrNoExecutables
	}

	if inBin := filterBinDirectory(root, found); len(inBin) > 0 {
		found = inBin
	} else {
		found = filterShallowest(root, found)
	}
	slices.Sort(found)
	return found, nil
}

// isProgram reports whether a file is a program for the target platform.
func isProgram(name string, mode fs.FileMode, goos string) bool {
	if goos == "windows" {
		return slices.Contains(windowsExtensions, strings.ToLower(filepath.Ext(name)))
	}
	return mode.Perm()&0o111 != 0 && !sharedLibrary.MatchString(strings.ToLower(name))
}

// filterBinDirectory keeps only programs stored directly in a bin directory.
func filterBinDirectory(root string, paths []string) []string {
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.EqualFold(filepath.Base(filepath.Dir(path)), "bin") && path != root {
			kept = append(kept, path)
		}
	}
	return kept
}

// filterShallowest keeps the programs closest to the root of the archive.
func filterShallowest(root string, paths []string) []string {
	shallowest := -1
	for _, path := range paths {
		if depth := pathDepth(root, path); shallowest < 0 || depth < shallowest {
			shallowest = depth
		}
	}
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if pathDepth(root, path) == shallowest {
			kept = append(kept, path)
		}
	}
	return kept
}

// pathDepth counts the directories between root and path.
func pathDepth(root, path string) int {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return 0
	}
	return strings.Count(filepath.ToSlash(relative), "/")
}
