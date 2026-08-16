package main

import (
	"fmt"
	"strings"
)

// boolFlags take no value, which the pre-pass needs to know so it can tell a
// flag's argument apart from a positional. Being listed here says only that a
// spelling never consumes the next argument, not that fit accepts it: help and
// version are subcommands, and `fit --version` is left to the FlagSet to
// reject by name rather than swallowing the filename after it.
var boolFlags = map[string]bool{
	"n": true, "dry-run": true,
	"f": true, "force": true,
	"json": true,
	"v":    true, "verbose": true,
	"h": true, "help": true,
	"version":   true,
	"no-preset": true,
}

// splitArgs lifts flags out of the argument list wherever they appear, so
// `fit chat clip.mp4 --under 8M` works without a CLI framework. stdlib flag
// stops at the first positional, which is why this runs first.
func splitArgs(args []string) (flags, pos []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			return flags, pos, nil
		}
		if len(a) > 1 && a[0] == '-' {
			name := strings.TrimLeft(a, "-")
			if strings.ContainsRune(name, '=') {
				flags = append(flags, a)
				continue
			}
			flags = append(flags, a)
			if boolFlags[name] {
				continue
			}
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag %s needs a value", a)
			}
			i++
			flags = append(flags, args[i])
			continue
		}
		pos = append(pos, a)
	}
	return flags, pos, nil
}
