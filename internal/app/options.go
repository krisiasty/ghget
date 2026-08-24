package app

import (
	"fmt"
	"strings"

	"github.com/krisiasty/ghget/internal/matcher"
)

type options struct {
	target       string
	directory    string
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
		case "-l", "--list":
			opts.listAssets = true
		case "-t", "--tag", "--tags":
			opts.listTags = true
		case "-g", "--glob":
			if modeSet {
				return opts, fmt.Errorf("--glob and --regex are mutually exclusive")
			}
			opts.mode, modeSet = matcher.Glob, true
		case "-r", "--regex":
			if modeSet {
				return opts, fmt.Errorf("--glob and --regex are mutually exclusive")
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
			opts.directory = v
		case "-o", "--output":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.output = v
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
		return opts, fmt.Errorf("expected one OWNER/REPO[/FILE][@TAG] argument")
	}
	if len(positional) == 1 {
		opts.target = positional[0]
	}
	if opts.help {
		return opts, nil
	}
	if opts.target == "" {
		return opts, fmt.Errorf("missing OWNER/REPO[/FILE][@TAG] argument")
	}
	if opts.listAssets && opts.listTags {
		return opts, fmt.Errorf("--list and --tag are mutually exclusive")
	}
	if opts.output != "" && opts.extract {
		return opts, fmt.Errorf("--output cannot be used with --extract")
	}
	if opts.keep && !opts.extract {
		return opts, fmt.Errorf("--keep requires --extract")
	}
	if opts.flat && !opts.extract {
		return opts, fmt.Errorf("--flat requires --extract")
	}
	return opts, nil
}
