package main

import (
	"os"

	"golang.org/x/term"
)

// isTTY reports whether the wrapper runs on a terminal. Both stdin and stdout
// are checked; any terminal side enables TTY mode for the OpenCode container.
func isTTY() bool {
	return isTerminal(os.Stdin) || isTerminal(os.Stdout)
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
