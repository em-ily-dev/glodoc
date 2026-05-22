// Package resolve interprets glodoc command-line arguments as a
// documentation rendering target.
//
// The argument grammar mirrors "go doc": zero arguments resolves to the
// current directory, one argument is tried in turn as a package path, a
// "pkg.symbol", a "pkg.type.method", and finally a fuzzy match by final
// path segment; two arguments are a package followed by "symbol" or
// "symbol.method".
package resolve

import (
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/token"
	"path"
	"strings"

	"golang.org/x/tools/go/packages"
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

// load loads the package at pkgPath and returns a Target. The package
// is loaded along with its test files so that examples can be attached
// to their documented subjects.
func load(pkgPath, sym, method string) (*Target, error) {
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedModule | packages.NeedImports,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, err
	}
	primary, xtest, err := pickVariants(pkgs)
	if err != nil {
		return nil, err
	}
	files := append([]*ast.File{}, primary.Syntax...)
	if xtest != nil {
		files = append(files, xtest.Syntax...)
	}
	docPkg, err := doc.NewFromFiles(primary.Fset, files, primary.PkgPath)
	if err != nil {
		return nil, err
	}
	return &Target{Pkg: docPkg, Fset: primary.Fset, Symbol: sym, Method: method}, nil
}

// pickVariants selects the package variant that carries the source +
// internal test files, along with the external _test package if any.
//
// packages.Load with Tests:true returns up to three variants: the base
// package, a "test variant" whose Syntax includes _test.go files, and a
// synthetic test binary. The external xxx_test package, when present,
// appears as a separate entry. The test binary (.test) is discarded.
func pickVariants(pkgs []*packages.Package) (primary, xtest *packages.Package, err error) {
	var base, withTests *packages.Package
	for _, p := range pkgs {
		if strings.HasSuffix(p.ID, ".test") {
			continue
		}
		if strings.HasSuffix(p.Name, "_test") {
			xtest = p
			continue
		}
		if strings.Contains(p.ID, ".test]") {
			withTests = p
		} else {
			base = p
		}
	}
	primary = withTests
	if primary == nil {
		primary = base
	}
	if primary == nil || primary.Name == "" {
		return nil, nil, errors.New("no Go package found")
	}
	if perr := firstError(primary); perr != nil {
		return nil, nil, perr
	}
	return primary, xtest, nil
}

// firstError returns the first non-list error reported for pkg, or nil.
//
// "List" errors (kind ListError) come from `go list` itself; we want to
// surface those as the resolution failure. Other errors usually indicate
// a parse or type-check problem we can render docs around, so we ignore
// them here.
func firstError(pkg *packages.Package) error {
	for _, e := range pkg.Errors {
		if e.Kind == packages.ListError {
			return errors.New(e.Msg)
		}
	}
	return nil
}

// fuzzy searches known packages (current module and stdlib) for one
// whose final path segment matches name, returning the first hit.
func fuzzy(name string) (*Target, error) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles}
	pkgs, err := packages.Load(cfg, "./...", "std")
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		if path.Base(p.PkgPath) == name {
			return load(p.PkgPath, "", "")
		}
	}
	return nil, fmt.Errorf("no package matching %q", name)
}
