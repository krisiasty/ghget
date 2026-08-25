// Package matcher selects release asset names using exact, glob, or regex patterns.
package matcher

import (
	"errors"
	"fmt"
	"path"
	"regexp"
)

// Mode identifies how an asset pattern is interpreted.
type Mode int

const (
	// Exact requires the complete asset name to equal the pattern.
	Exact Mode = iota
	// Glob interprets the pattern using path.Match syntax.
	Glob
	// Regex interprets the pattern as a Go regular expression.
	Regex
)

// Select returns names matching pattern in their original order.
func Select(names []string, pattern string, mode Mode) ([]string, error) {
	if pattern == "" {
		return nil, errors.New("file pattern is empty")
	}
	var matches func(string) (bool, error)
	switch mode {
	case Exact:
		matches = func(name string) (bool, error) { return name == pattern, nil }
	case Glob:
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
		matches = func(name string) (bool, error) { return path.Match(pattern, name) }
	case Regex:
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression %q: %w", pattern, err)
		}
		matches = func(name string) (bool, error) { return re.MatchString(name), nil }
	default:
		return nil, errors.New("unknown matching mode")
	}

	selected := make([]string, 0)
	for _, name := range names {
		ok, err := matches(name)
		if err != nil {
			return nil, err
		}
		if ok {
			selected = append(selected, name)
		}
	}
	return selected, nil
}
