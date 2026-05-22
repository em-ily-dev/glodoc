// Package resolve interprets glodoc command-line arguments as a
// documentation rendering target.
//
// The argument grammar mirrors "go doc": zero arguments resolves to the
// current directory, one argument is tried in turn as a package path, a
// "pkg.symbol", a "pkg.type.method", a fuzzy match by final path
// segment, and finally as a symbol of the current directory's package;
// two arguments are a package followed by "symbol" or "symbol.method".
//
// Package lookup is performed via go/build, the same mechanism go doc
// uses, so paths are resolved without the cost of dependency analysis.
package resolve

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ily.dev/glodoc/internal/modindex"
)

// Options controls the loading and indexing of package documentation.
type Options struct {
	// Unexported requests inclusion of unexported declarations,
	// methods, and fields. It corresponds to "go doc -u".
	Unexported bool
	// Source requests that parsed declarations retain their bodies
	// and surrounding details so the renderer can emit source code.
	// It corresponds to "go doc -src".
	Source bool
}

// Target identifies a resolved documentation rendering target.
type Target struct {
	// Pkg is the assembled documentation for the package.
	Pkg *doc.Package
	// Fset is the file set used to parse the package's source.
	Fset *token.FileSet
	// Symbol, if non-empty, narrows rendering to a single top-level symbol.
	Symbol string
	// Method, if non-empty, further narrows rendering to a method or
	// field on Symbol.
	Method string
}

// Resolve interprets args (0, 1, or 2 elements) as a rendering target.
//
// With zero arguments, the current directory is used. With one argument,
// the shapes "<pkg>", "<pkg>.<sym>", and "<pkg>.<type>.<method>" are tried
// in turn, followed by a fuzzy match against known packages by final path
// segment and finally as a symbol of the current directory's package.
// With two arguments, the first is the package and the second is
// "<sym>" or "<sym>.<method>".
func Resolve(args []string, opts Options) (*Target, error) {
	r := resolver{opts: opts}
	switch len(args) {
	case 0:
		return r.load(".", "", "")
	case 1:
		return r.resolveOne(args[0])
	case 2:
		sym, method := splitSym(args[1])
		return r.loadAny(args[0], sym, method)
	}
	return nil, fmt.Errorf("expected 0, 1, or 2 arguments, got %d", len(args))
}

// resolver carries the loading options through the resolution attempts.
type resolver struct {
	opts Options
}

// resolveOne resolves a single positional argument by trying it in
// progressively looser forms.
func (r *resolver) resolveOne(arg string) (*Target, error) {
	if t, err := r.loadAny(arg, "", ""); err == nil {
		return t, nil
	}
	if i := strings.LastIndex(arg, "."); i > 0 {
		if t, err := r.loadAny(arg[:i], arg[i+1:], ""); err == nil {
			return t, nil
		}
		if j := strings.LastIndex(arg[:i], "."); j > 0 {
			if t, err := r.loadAny(arg[:j], arg[j+1:i], arg[i+1:]); err == nil {
				return t, nil
			}
		}
	}
	if t, err := r.fuzzy(arg); err == nil {
		return t, nil
	}
	if t, err := r.currentDirSymbol(arg); err == nil {
		return t, nil
	}
	return nil, fmt.Errorf("could not resolve %q", arg)
}

// loadAny tries pkgPath as given, then with a "./" prefix if it isn't
// already a relative or absolute path. This lets bare paths like
// "internal/foo" resolve as filesystem-relative when no import path of
// that name exists.
func (r *resolver) loadAny(pkgPath, sym, method string) (*Target, error) {
	if t, err := r.load(pkgPath, sym, method); err == nil {
		return t, nil
	} else if isPath(pkgPath) {
		return nil, err
	}
	return r.load("./"+pkgPath, sym, method)
}

// isPath reports whether s already looks like a filesystem path (so we
// shouldn't try prepending "./").
func isPath(s string) bool {
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "/")
}

// splitSym splits "sym" or "sym.method" into its parts.
func splitSym(s string) (sym, method string) {
	sym, method, _ = strings.Cut(s, ".")
	return sym, method
}

// load resolves pkgPath via go/build, parses the package's Go files
// (including test files, so examples attach to their subjects), and
// returns a Target.
func (r *resolver) load(pkgPath, sym, method string) (*Target, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	bpkg, err := build.Default.Import(pkgPath, cwd, build.ImportComment)
	if err != nil {
		return nil, err
	}
	return r.fromBuildPackage(bpkg, sym, method)
}

// fromBuildPackage parses the Go source of bpkg and assembles a Target.
func (r *resolver) fromBuildPackage(bpkg *build.Package, sym, method string) (*Target, error) {
	fset := token.NewFileSet()
	var files []*ast.File
	parseList := func(dir string, names []string) error {
		for _, name := range names {
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
			if err != nil {
				return err
			}
			files = append(files, f)
		}
		return nil
	}
	if err := parseList(bpkg.Dir, bpkg.GoFiles); err != nil {
		return nil, err
	}
	if err := parseList(bpkg.Dir, bpkg.TestGoFiles); err != nil {
		return nil, err
	}
	if err := parseList(bpkg.Dir, bpkg.XTestGoFiles); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files in %s", bpkg.Dir)
	}
	importPath := bpkg.ImportPath
	if importPath == "" || importPath == "." {
		// build.Import doesn't always set ImportPath for filesystem
		// targets; fall back to the directory name for display.
		importPath = filepath.Base(bpkg.Dir)
	}
	docPkg, err := doc.NewFromFiles(fset, files, importPath, r.docMode()...)
	if err != nil {
		return nil, err
	}
	return &Target{Pkg: docPkg, Fset: fset, Symbol: sym, Method: method}, nil
}

// docMode returns the combined doc.Mode bitmask implied by the
// resolver options, packaged as the single variadic argument expected
// by doc.NewFromFiles.
func (r *resolver) docMode() []any {
	var mode doc.Mode
	if r.opts.Unexported {
		mode |= doc.AllDecls | doc.AllMethods
	}
	if r.opts.Source {
		// PreserveAST keeps function bodies and unmodified declarations
		// so the renderer can emit the original Go source.
		mode |= doc.PreserveAST
	}
	if mode == 0 {
		return nil
	}
	return []any{mode}
}

// fuzzy searches indexed package directories for one whose final path
// segment matches name and returns its rendering target.
func (r *resolver) fuzzy(name string) (*Target, error) {
	hits := modindex.Default().FindByBase(name)
	if len(hits) == 0 {
		return nil, fmt.Errorf("no package matching %q", name)
	}
	bpkg, err := build.Default.ImportDir(hits[0].Dir, build.ImportComment)
	if err != nil {
		return nil, err
	}
	if bpkg.ImportPath == "" {
		bpkg.ImportPath = hits[0].ImportPath
	}
	return r.fromBuildPackage(bpkg, "", "")
}

// currentDirSymbol tries to resolve arg as a symbol (or "sym.method")
// in the package of the current working directory. It succeeds only
// when the symbol is actually present, so callers can chain this after
// other resolution attempts without falsely matching every input.
func (r *resolver) currentDirSymbol(arg string) (*Target, error) {
	t, err := r.load(".", "", "")
	if err != nil {
		return nil, err
	}
	sym, method := splitSym(arg)
	if !packageHasSymbol(t.Pkg, sym) {
		return nil, fmt.Errorf("no symbol %q in current package", sym)
	}
	t.Symbol = sym
	t.Method = method
	return t, nil
}

// packageHasSymbol reports whether the package exposes a top-level
// symbol with the given name. Matching is case-insensitive because
// case-sensitivity is a rendering-time concern controlled by -c.
func packageHasSymbol(pkg *doc.Package, name string) bool {
	if name == "" {
		return false
	}
	eq := func(s string) bool { return strings.EqualFold(s, name) }
	for _, c := range pkg.Consts {
		if slices.ContainsFunc(c.Names, eq) {
			return true
		}
	}
	for _, v := range pkg.Vars {
		if slices.ContainsFunc(v.Names, eq) {
			return true
		}
	}
	for _, f := range pkg.Funcs {
		if eq(f.Name) {
			return true
		}
	}
	for _, t := range pkg.Types {
		if eq(t.Name) {
			return true
		}
	}
	return false
}
