// Command glodoc renders Go package documentation as styled markdown.
//
// It mirrors the shape of glow: with arguments, it prints the
// documentation for a package or symbol; with no arguments, it opens a
// TUI listing the packages of the current module.
//
// Examples:
//
//	glodoc fmt
//	glodoc fmt.Println
//	glodoc bytes Buffer.WriteString
//	glodoc ./internal/render
package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/glamour"
	"golang.org/x/term"

	"ily.dev/glodoc/internal/pager"
	"ily.dev/glodoc/internal/render"
	"ily.dev/glodoc/internal/resolve"
	"ily.dev/glodoc/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "glodoc:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runTUI()
	}
	return renderOnce(args)
}

// renderOnce resolves the arguments, renders the result through
// glamour, and writes it out (paginating if attached to a terminal).
func renderOnce(args []string) error {
	target, err := resolve.Resolve(args)
	if err != nil {
		return err
	}
	md := render.Package(target.Pkg, target.Fset, target.Symbol, target.Method)
	out, err := styled(md)
	if err != nil {
		return err
	}
	return pager.Write(out)
}

// styled renders markdown through glamour using an auto-detected style
// that respects the terminal background.
func styled(md string) (string, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(termWidth()),
	)
	if err != nil {
		return "", err
	}
	defer r.Close()
	return r.Render(md)
}

// runTUI starts the interactive package browser for the current module.
func runTUI() error {
	return tui.Run()
}

// termWidth reports a reasonable word-wrap width, clamped to a maximum
// of 100 columns so prose remains comfortable to read on wide terminals.
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return min(w, 100)
	}
	return 80
}
