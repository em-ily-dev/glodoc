// Command glodoc renders Go package documentation in color.
//
// It mirrors the shape of glow: with arguments, it prints the
// documentation for a package or symbol; with no arguments, it opens a
// TUI listing the packages of the current module.
//
// The flag and positional-argument grammar matches "go doc", and the
// rendered text is go doc's exactly — styling is the only addition, and
// output to a pipe carries no styling at all — so glodoc can be used as
// a drop-in replacement.
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
	"strings"

	"github.com/muesli/termenv"
	"golang.org/x/term"

	"ily.dev/glodoc/internal/pager"
	"ily.dev/glodoc/internal/render"
	"ily.dev/glodoc/internal/resolve"
	"ily.dev/glodoc/internal/style"
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
		return tui.Run()
	}
	return renderOnce(args)
}

// renderOnce resolves the arguments, renders the documentation they
// select, and writes it out (paginating if attached to a terminal).
// It is a port of cmd/go/internal/doc.do's main loop: when a resolved
// package does not contain the requested symbol, resolution continues
// to the next candidate package — this is how "glodoc rand.Intn" scans
// past crypto/rand to math/rand — until something is printed or the
// candidates are exhausted.
func renderOnce(args []string) error {
	baseOpts := render.Options{
		All:           *flagAll,
		Short:         *flagShort,
		Src:           *flagSrc,
		Unexported:    *flagU,
		CaseSensitive: *flagCase,
		ShowCmd:       *flagCmd,
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		baseOpts.Style = style.New(detectTheme())
	}

	var resolver resolve.Resolver
	var paths []string
	var symbol, method string
	// Loop until something is printed.
	for i := 0; ; i++ {
		target, more, err := resolver.Resolve(args)
		if i > 0 && !more { // Ignore the "more" bit on the first iteration.
			return failMessage(paths, symbol, method)
		}
		if err != nil {
			return err
		}
		symbol, method = target.Symbol, target.Method

		opts := baseOpts
		// The builtin package needs special treatment: its symbols are
		// lower case but we want to see them, always.
		if target.Build.ImportPath == "builtin" {
			opts.Unexported = true
		}
		pkg, err := render.New(target.Build, target.UserPath, opts)
		if err != nil {
			return err
		}
		paths = append(paths, pkg.PrettyPath())

		out, found, err := pkg.Render(symbol, method)
		if err != nil {
			return err
		}
		if found {
			return pager.Write(out)
		}
	}
}

// failMessage creates a nicely formatted error message when there is no
// result to show, mirroring go doc's failMessage.
func failMessage(paths []string, symbol, method string) error {
	var b strings.Builder
	if len(paths) > 1 {
		b.WriteString("s")
	}
	b.WriteString(" ")
	for i, path := range paths {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(path)
	}
	if method == "" {
		return fmt.Errorf("no symbol %s in package%s", symbol, b.String())
	}
	return fmt.Errorf("no method or field %s.%s in package%s", symbol, method, b.String())
}

// detectTheme probes the terminal background and returns the style
// theme to use, "dark" or "light".
func detectTheme() string {
	if termenv.NewOutput(os.Stdout).HasDarkBackground() {
		return "dark"
	}
	return "light"
}
