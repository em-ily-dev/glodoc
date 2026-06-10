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
// The dispatch mirrors cmd/go/internal/doc.do.
func renderOnce(args []string) error {
	target, err := resolve.Resolve(args)
	if err != nil {
		return err
	}
	opts := render.Options{
		All:           *flagAll,
		Short:         *flagShort,
		Src:           *flagSrc,
		Unexported:    *flagU,
		CaseSensitive: *flagCase,
		ShowCmd:       *flagCmd,
	}
	// The builtin package needs special treatment: its symbols are
	// lower case but we want to see them, always.
	if target.Build.ImportPath == "builtin" {
		opts.Unexported = true
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		opts.Style = style.New(detectTheme())
	}
	pkg, err := render.New(target.Build, target.UserPath, opts)
	if err != nil {
		return err
	}
	out, found, err := pkg.Render(target.Symbol, target.Method)
	if err != nil {
		return err
	}
	if !found {
		return failMessage(pkg.PrettyPath(), target.Symbol, target.Method)
	}
	return pager.Write(out)
}

// failMessage mirrors go doc's failMessage for a single package path.
func failMessage(path, symbol, method string) error {
	if method == "" {
		return fmt.Errorf("no symbol %s in package %s", symbol, path)
	}
	return fmt.Errorf("no method or field %s.%s in package %s", symbol, method, path)
}

// detectTheme probes the terminal background and returns the style
// theme to use, "dark" or "light".
func detectTheme() string {
	if termenv.NewOutput(os.Stdout).HasDarkBackground() {
		return "dark"
	}
	return "light"
}
