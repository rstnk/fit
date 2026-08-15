package ui

import (
	"os"
	"runtime"
)

// isTerminal reports whether f is a character device, which is close enough to
// a TTY for deciding whether to colour output.
func isTerminal(f *os.File) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// wantsColour reports whether f should carry ANSI colour, honouring the
// NO_COLOR convention (https://no-color.org, any non-empty value disables
// colour) and TERM=dumb ahead of the terminal check itself.
func wantsColour(f *os.File) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(f)
}
