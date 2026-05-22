// Package resolve interprets glodoc command-line arguments as a
// documentation rendering target, following the algorithm used by
// "go doc".
//
// The argument grammar mirrors go doc: zero arguments resolves to the
// current directory; one argument is tried as a complete package path,
// then (if the first letter is upper case) as a symbol in the current
// directory, then progressively shorter "<pkg>.<sym>" splits, with a
// fuzzy lookup by trailing path segment as the fallback at each step;
// two arguments are a package followed by "<sym>[.<methodOrField>]".
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
	"path"
	"path/filepath"
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

// Resolve interprets args (0, 1, or 2 elements) as a rendering target,
// following go doc's parseArgs algorithm.
func Resolve(args []string, opts Options) (*Target, error) {
	r := resolver{opts: opts}
	return r.parseArgs(args)
}

// LoadDir loads documentation for the package at the given filesystem
// directory, bypassing import-path resolution. It is intended for
// callers (such as the TUI) that already know the package's location
// on disk and want to avoid the cost of consulting the module graph.
func LoadDir(dir string, opts Options) (*Target, error) {
	r := resolver{opts: opts}
	bpkg, err := build.Default.ImportDir(dir, build.ImportComment)
	if err != nil {
		return nil, err
	}
	return r.fromBuildPackage(bpkg, "", "")
}

// resolver carries the loading options through the resolution attempts.
type resolver struct {
	opts Options
}

// parseArgs is a port of cmd/go/internal/doc.parseArgs. It returns the
// first target found by walking the same sequence of fallbacks go doc
// itself uses.
func (r *resolver) parseArgs(args []string) (*Target, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return r.fromDir(wd, "", "")
	}

	arg := args[0]
	// Convert a leading "./" or "../" to an absolute path so we don't
	// confuse a local "./errors" with the standard "errors" package.
	if isDotSlash(arg) {
		arg = filepath.Join(wd, arg)
	}

	switch len(args) {
	case 2:
		sym, method := splitSym(args[1])
		if bpkg, err := build.Default.Import(args[0], wd, build.ImportComment); err == nil {
			return r.fromBuildPackage(bpkg, sym, method)
		}
		if t, err := r.fuzzy(arg, sym, method); err == nil {
			return t, nil
		}
		return nil, fmt.Errorf("no such package: %s", args[0])
	case 1:
		// fall through to the one-arg logic below
	default:
		return nil, fmt.Errorf("expected at most 2 arguments, got %d", len(args))
	}

	// One-arg case. Try the arg as a complete package path first;
	// this short-circuits the period splits below so a package path
	// that contains another package path as a prefix can't be
	// confused for "<prefix>.<rest>".
	var importErr error
	if filepath.IsAbs(arg) {
		bpkg, err := build.Default.ImportDir(arg, build.ImportComment)
		if err == nil {
			return r.fromBuildPackage(bpkg, "", "")
		}
		importErr = err
	} else {
		bpkg, err := build.Default.Import(arg, wd, build.ImportComment)
		if err == nil {
			return r.fromBuildPackage(bpkg, "", "")
		}
		importErr = err
	}

	// If arg starts with an upper-case letter and has no slashes, it
	// can only be a symbol in the current directory. This kills the
	// problem of case-insensitive filesystems matching an upper-case
	// name as a package name.
	if !strings.ContainsAny(arg, `/\`) && token.IsExported(arg) {
		sym, method := splitSym(arg)
		if t, err := r.fromDir(".", sym, method); err == nil {
			return t, nil
		}
	}

	// Try period splits after the last slash, looking for "<pkg>.<sym>".
	slash := strings.LastIndex(arg, "/")
	period := -1
	for start := slash + 1; start < len(arg); start = period + 1 {
		rel := strings.Index(arg[start:], ".")
		var rest string
		if rel < 0 {
			period = len(arg)
		} else {
			period = start + rel
			rest = arg[period+1:]
		}
		pkgPath := arg[:period]
		sym, method := splitSym(rest)
		if bpkg, err := build.Default.Import(pkgPath, wd, build.ImportComment); err == nil {
			return r.fromBuildPackage(bpkg, sym, method)
		}
		if t, err := r.fuzzy(pkgPath, sym, method); err == nil {
			return t, nil
		}
	}

	// If the original arg had a slash, no match is fatal: it can only
	// have been a package path.
	if slash >= 0 {
		return nil, importErr
	}

	// Last resort: assume a symbol in the current directory.
	sym, method := splitSym(arg)
	return r.fromDir(".", sym, method)
}

// fromDir parses the package rooted at dir and returns a Target.
func (r *resolver) fromDir(dir, sym, method string) (*Target, error) {
	bpkg, err := build.Default.ImportDir(dir, build.ImportComment)
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
		mode |= doc.PreserveAST
	}
	if mode == 0 {
		return nil
	}
	return []any{mode}
}

// fuzzy searches indexed package directories for one whose import
// path is or ends with name, matching go doc's findNextPackage. It
// returns the first hit; an upper-case name returns no match because
// it cannot be a package name.
func (r *resolver) fuzzy(name, sym, method string) (*Target, error) {
	if name == "" || token.IsExported(name) {
		return nil, fmt.Errorf("no package matching %q", name)
	}
	name = path.Clean(name)
	suffix := "/" + name
	for _, e := range modindex.Default().All() {
		if e.ImportPath == name || strings.HasSuffix(e.ImportPath, suffix) {
			bpkg, err := build.Default.ImportDir(e.Dir, build.ImportComment)
			if err != nil {
				continue
			}
			if bpkg.ImportPath == "" || bpkg.ImportPath == "." {
				bpkg.ImportPath = e.ImportPath
			}
			return r.fromBuildPackage(bpkg, sym, method)
		}
	}
	return nil, fmt.Errorf("no package matching %q", name)
}

// splitSym splits "sym" or "sym.method" into its parts.
func splitSym(s string) (sym, method string) {
	sym, method, _ = strings.Cut(s, ".")
	return sym, method
}

// isDotSlash reports whether s begins with a reference to the local
// "." or ".." directory. It matches the eponymous helper in
// cmd/go/internal/doc/doc.go.
func isDotSlash(s string) bool {
	if s == "." || s == ".." {
		return true
	}
	for _, prefix := range []string{"./", "../", `.\`, `..\`} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
