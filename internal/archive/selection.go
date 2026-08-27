package archive

import (
	"fmt"
	"sort"
	"strings"
)

type fileSelection struct {
	wanted  map[string]struct{}
	found   map[string]struct{}
	targets map[string]string
	used    map[string]string
	flat    bool
}

func newFileSelection(files []string, flat bool) (*fileSelection, error) {
	if len(files) == 0 {
		return nil, nil
	}
	selection := &fileSelection{
		wanted:  make(map[string]struct{}, len(files)),
		found:   make(map[string]struct{}, len(files)),
		targets: make(map[string]string, len(files)),
		used:    make(map[string]string, len(files)),
		flat:    flat,
	}
	for _, name := range files {
		if _, err := cleanArchivePath(name); err != nil {
			return nil, fmt.Errorf("invalid selected archive member: %w", err)
		}
		selection.wanted[name] = struct{}{}
	}
	return selection, nil
}

func (s *fileSelection) wants(name string) bool {
	_, wanted := s.wanted[name]
	return wanted
}

func (s *fileSelection) addRegular(name string) (string, error) {
	if _, duplicate := s.found[name]; duplicate {
		return "", fmt.Errorf("archive contains duplicate selected member %q", name)
	}
	clean, err := cleanArchivePath(name)
	if err != nil {
		return "", err
	}
	target := archiveTarget(clean, s.flat)
	if other, collision := s.used[target]; collision && other != name {
		return "", fmt.Errorf("selected archive members %q and %q both target %q", other, name, target)
	}
	s.found[name] = struct{}{}
	s.targets[name] = target
	s.used[target] = name
	return target, nil
}

func (s *fileSelection) rejectNonRegular(name string) error {
	return fmt.Errorf("requested archive member %q is not a regular file", name)
}

func (s *fileSelection) finish() error {
	missing := make([]string, 0)
	for name := range s.wanted {
		if _, found := s.found[name]; !found {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("requested archive members not found: %s", strings.Join(missing, ", "))
}
