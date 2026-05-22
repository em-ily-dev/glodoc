// Package resolve interprets glodoc command-line arguments as a
// documentation rendering target.
//
// The argument grammar mirrors "go doc": zero arguments resolves to the
// current directory, one argument is tried in turn as a package path, a
// "pkg.symbol", a "pkg.type.method", and finally a fuzzy match by final
// path segment; two arguments are a package followed by "symbol" or
// "symbol.method".
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
	"strings"

	"ily.dev/glodoc/internal/modindex"
)

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
// segment. With two arguments, the first is the package and the second is
// "<sym>" or "<sym>.<method>".
func Resolve(args []string) (*Target, error) {
	switch len(args) {
	case 0:
		return load(".", "", "")
	case 1:
		return resolveOne(args[0])
	case 2:
		sym, method := splitSym(args[1])
		return loadAny(args[0], sym, method)
	}
	return nil, fmt.Errorf("expected 0, 1, or 2 arguments, got %d", len(args))
}

// resolveOne resolves a single positional argument by trying it in
// progressively looser forms.
func resolveOne(arg string) (*Target, error) {
	if t, err := loadAny(arg, "", ""); err == nil {
		return t, nil
	}
	if i := strings.LastIndex(arg, "."); i > 0 {
		if t, err := loadAny(arg[:i], arg[i+1:], ""); err == nil {
			return t, nil
		}
		if j := strings.LastIndex(arg[:i], "."); j > 0 {
			if t, err := loadAny(arg[:j], arg[j+1:i], arg[i+1:]); err == nil {
				return t, nil
			}
		}
	}
	if t, err := fuzzy(arg); err == nil {
		return t, nil
	}
	return nil, fmt.Errorf("could not resolve %q", arg)
}

// loadAny tries pkgPath as given, then with a "./" prefix if it isn't
// already a relative or absolute path. This lets bare paths like
// "internal/foo" resolve as filesystem-relative when no import path of
// that name exists.
func loadAny(pkgPath, sym, method string) (*Target, error) {
	if t, err := load(pkgPath, sym, method); err == nil {
		return t, nil
	} else if isPath(pkgPath) {
		return nil, err
	}
	return load("./"+pkgPath, sym, method)
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
func load(pkgPath, sym, method string) (*Target, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	bpkg, err := build.Default.Import(pkgPath, cwd, build.ImportComment)
	if err != nil {
		return nil, err
	}
	return fromBuildPackage(bpkg, sym, method)
}

// fromBuildPackage parses the Go source of bpkg and assembles a Target.
func fromBuildPackage(bpkg *build.Package, sym, method string) (*Target, error) {
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
	docPkg, err := doc.NewFromFiles(fset, files, importPath)
	if err != nil {
		return nil, err
	}
	return &Target{Pkg: docPkg, Fset: fset, Symbol: sym, Method: method}, nil
}

// fuzzy searches indexed package directories for one whose final path
// segment matches name and returns its rendering target.
func fuzzy(name string) (*Target, error) {
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
	return fromBuildPackage(bpkg, "", "")
}
