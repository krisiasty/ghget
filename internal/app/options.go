package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/krisiasty/ghget/internal/matcher"
)

type options struct {
	target       string
	directory    string
	directorySet bool
	upgrade      bool
	auto         bool
	install      bool
	first        bool
	output       string
	checksum     string
	mode         matcher.Mode
	listAssets   bool
	listTags     bool
	extract      bool
	executable   bool
	unquarantine bool
	force        bool
	keep         bool
	flat         bool
	debug        bool
	help         bool
	version      bool
}

func parseOptions(args []string) (options, error) {
	opts := options{directory: "."}
	positional := make([]string, 0, 1)
	modeSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := strings.Cut(arg, "=")
		value := func() (string, error) {
			if hasInline {
				if inline == "" {
					return "", fmt.Errorf("option %s requires a value", name)
				}
				return inline, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("option %s requires a value", name)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "-h", "--help":
			opts.help = true
		case "--version":
			opts.version = true
		case "--upgrade":
			opts.upgrade = true
		case "-a", "--auto":
			opts.auto = true
		case "-i", "--install":
			opts.install = true
		case "--first":
			opts.first = true
		case "-l", "--list":
			opts.listAssets = true
		case "-t", "--tag", "--tags":
			opts.listTags = true
		case "-g", "--glob":
			if modeSet {
				return opts, errors.New("--glob and --regex are mutually exclusive")
			}
			opts.mode, modeSet = matcher.Glob, true
		case "-r", "--regex":
			if modeSet {
				return opts, errors.New("--glob and --regex are mutually exclusive")
			}
			opts.mode, modeSet = matcher.Regex, true
		case "-e", "--extract":
			opts.extract = true
		case "-x", "--executable":
			opts.executable = true
		case "-u", "--unquarantine":
			opts.unquarantine = true
		case "-f", "--force":
			opts.force = true
		case "-k", "--keep":
			opts.keep = true
		case "--flat":
			opts.flat = true
		case "--debug":
			opts.debug = true
		case "-d", "--dir":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.directory, err = expandHomePath(v)
			if err != nil {
				return opts, err
			}
			opts.directorySet = true
		case "-o", "--output":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.output, err = expandHomePath(v)
			if err != nil {
				return opts, err
			}
		case "-c", "--checksum":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.checksum = v
		case "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown option %s", arg)
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) > 1 {
		return opts, errors.New("expected one OWNER/REPO[/FILE][@TAG] argument")
	}
	if len(positional) == 1 {
		opts.target = positional[0]
	}
	if opts.help || opts.version {
		return opts, nil
	}
	if opts.upgrade {
		return opts, validateUpgrade(opts, modeSet)
	}
	if opts.target == "" {
		return opts, errors.New("missing OWNER/REPO[/FILE][@TAG] argument")
	}
	if opts.listAssets && opts.listTags {
		return opts, errors.New("--list and --tag are mutually exclusive")
	}
	if opts.output != "" && opts.extract {
		return opts, errors.New("--output cannot be used with --extract")
	}
	if opts.keep && !opts.extract && !opts.install {
		return opts, errors.New("--keep requires --extract or --install")
	}
	if opts.flat && !opts.extract {
		return opts, errors.New("--flat requires --extract")
	}
	if err := validateAuto(opts, modeSet); err != nil {
		return opts, err
	}
	return opts, nil
}

// validateAuto rejects combinations that would give --auto or --install
// nothing to do, or two conflicting answers to the same question.
func validateAuto(opts options, modeSet bool) error {
	// --install decides what lands on disk, which is what --extract also decides.
	if opts.install && opts.extract {
		return errors.New("--install cannot be combined with --extract")
	}
	if opts.first && !opts.auto {
		return errors.New("--first requires --auto; it resolves an ambiguous automatic selection")
	}
	if !opts.auto {
		return nil
	}
	conflicts := []struct {
		set  bool
		name string
	}{
		{opts.listAssets, "--list"},
		{opts.listTags, "--tag"},
		{modeSet, "--glob or --regex"},
	}
	for _, conflict := range conflicts {
		if conflict.set {
			return fmt.Errorf("--auto cannot be combined with %s", conflict.name)
		}
	}
	return nil
}

func expandHomePath(value string) (string, error) {
	remainder, expand := homePathRemainder(value)
	if !expand {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	if remainder == "" {
		return home, nil
	}
	return filepath.Join(home, filepath.FromSlash(remainder)), nil
}

func homePathRemainder(value string) (string, bool) {
	for _, prefix := range []string{"~", "$HOME", "${HOME}"} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		if value == prefix {
			return "", true
		}
		if len(value) > len(prefix) && os.IsPathSeparator(value[len(prefix)]) {
			remainder := value[len(prefix)+1:]
			for len(remainder) > 0 && os.IsPathSeparator(remainder[0]) {
				remainder = remainder[1:]
			}
			return remainder, true
		}
	}
	return "", false
}

// validateUpgrade rejects options that cannot apply to a self-upgrade, which
// derives its own target, destination, and permissions.
func validateUpgrade(opts options, modeSet bool) error {
	if opts.target != "" {
		return fmt.Errorf("--upgrade does not take a target; use OWNER/REPO/FILE to download %q", opts.target)
	}
	conflicts := []struct {
		set  bool
		name string
	}{
		{opts.listAssets, "--list"},
		{opts.listTags, "--tag"},
		{opts.extract, "--extract"},
		{opts.keep, "--keep"},
		{opts.flat, "--flat"},
		{opts.output != "", "--output"},
		{opts.directorySet, "--dir"},
		{opts.checksum != "", "--checksum"},
		{opts.executable, "--executable"},
		{opts.unquarantine, "--unquarantine"},
		{opts.auto, "--auto"},
		{opts.install, "--install"},
		{opts.first, "--first"},
		{modeSet, "--glob or --regex"},
	}
	for _, conflict := range conflicts {
		if conflict.set {
			return fmt.Errorf("--upgrade cannot be combined with %s", conflict.name)
		}
	}
	return nil
}
