// Command glodoc renders Go package documentation as styled markdown.
//
// It mirrors the shape of glow: with arguments, it prints the
// documentation for a package or symbol; with no arguments, it opens a
// TUI listing the packages of the current module.
//
// The flag and positional-argument grammar matches "go doc" so glodoc
// can be used as a drop-in replacement.
//
// Examples:
//
//	glodoc fmt
//	glodoc fmt.Println
//	glodoc bytes Buffer.WriteString
//	glodoc -all errors
//	glodoc -short fmt
//	glodoc -src fmt.Println
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/charmbracelet/glamour"
	"golang.org/x/term"

	"ily.dev/glodoc/internal/pager"
	"ily.dev/glodoc/internal/render"
	"ily.dev/glodoc/internal/resolve"
	"ily.dev/glodoc/internal/tui"
)

// The flag set mirrors "go doc"'s, both in option names and in the
// exact wording of each flag's description (modulo "-http", which
// glodoc does not implement). See cmd/go/internal/doc.do for the
// reference.
var (
	flagChdir = flag.String("C", "", "change to `dir` before running command")
	flagAll   = flag.Bool("all", false, "show all documentation for package")
	flagCase  = flag.Bool("c", false, "symbol matching honors case (paths not affected)")
	flagCmd   = flag.Bool("cmd", false, "show symbols with package docs even if package is a command")
	flagShort = flag.Bool("short", false, "one-line representation for each symbol")
	flagSrc   = flag.Bool("src", false, "show source code for symbol")
	flagU     = flag.Bool("u", false, "show unexported symbols as well as exported")
)

func main() {
	flag.Usage = usage
	flag.Parse()
	if *flagChdir != "" {
		if err := os.Chdir(*flagChdir); err != nil {
			fmt.Fprintln(os.Stderr, "glodoc:", err)
			os.Exit(1)
		}
	}
	if err := run(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "glodoc:", err)
		os.Exit(1)
	}
}

// usage prints the command's help text. The wording mirrors
// "go doc -h" so users carrying over muscle memory see what they
// expect; the command name is the only substitution.
func usage() {
	fmt.Fprintln(os.Stderr, "Usage of glodoc:")
	fmt.Fprintln(os.Stderr, "\tglodoc")
	fmt.Fprintln(os.Stderr, "\tglodoc <pkg>")
	fmt.Fprintln(os.Stderr, "\tglodoc <sym>[.<methodOrField>]")
	fmt.Fprintln(os.Stderr, "\tglodoc [<pkg>.]<sym>[.<methodOrField>]")
	fmt.Fprintln(os.Stderr, "\tglodoc [<pkg>.][<sym>.]<methodOrField>")
	fmt.Fprintln(os.Stderr, "\tglodoc <pkg> <sym>[.<methodOrField>]")
	fmt.Fprintln(os.Stderr, "For more information run")
	fmt.Fprintln(os.Stderr, "\tgo help doc")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
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
	target, err := resolve.Resolve(args, resolve.Options{
		Unexported: *flagU,
		Source:     *flagSrc,
	})
	if err != nil {
		return err
	}
	md := render.Package(target.Pkg, target.Fset, target.Symbol, target.Method, render.Options{
		All:           *flagAll,
		Short:         *flagShort,
		Src:           *flagSrc,
		Unexported:    *flagU,
		CaseSensitive: *flagCase,
		IncludeMain:   *flagCmd,
	})
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
