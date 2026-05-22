// Package pager pipes rendered output through the user's preferred
// pager when standard output is a terminal.
//
// The pager is selected from $PAGER, falling back to "less -FIRX" so that
// short output prints inline and ANSI styling is preserved.
package pager

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// Write writes s to standard output, piping through $PAGER when stdout
// is a terminal. If stdout is not a terminal, s is written directly.
//
// The fallback pager is "less -FIRX": -F exits immediately when output
// fits on one screen, -R preserves ANSI styling, -X suppresses the
// terminal init sequence (so the output remains visible on exit), and
// -I makes searches case-insensitive.
func Write(s string) error {
	if !isTerminal(os.Stdout) {
		_, err := io.WriteString(os.Stdout, s)
		return err
	}
	pager := strings.TrimSpace(os.Getenv("PAGER"))
	if pager == "" {
		pager = "less -FIRX"
	}
	cmd := exec.Command("sh", "-c", pager)
	cmd.Stdin = strings.NewReader(s)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// isTerminal reports whether f refers to a character device.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
