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
// A fully qualified or relative package path is resolved with go/build,
// the same mechanism go doc uses. A bare or partial path is matched
// against an index of the standard library, the current module, and—only
// when those do not match—the current module's dependencies; see package
// modindex.
//
// Resolution stops at locating the package: the result identifies a
// directory of Go source and the symbol selectors, and parsing is left
// to the renderer, mirroring the seam between go doc's parseArgs and
// parsePackage.
package resolve

import (
	"fmt"
	"go/build"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"ily.dev/glodoc/internal/modindex"
)

// Target identifies a resolved documentation rendering target.
type Target struct {
	// Build locates the package's source on disk.
	Build *build.Package
	// UserPath is the string the user used to identify the package, or
	// empty if the package is implied by the current directory.
	UserPath string
	// Symbol, if non-empty, narrows rendering to a single top-level symbol.
	Symbol string
	// Method, if non-empty, further narrows rendering to a method or
	// field on Symbol.
	Method string
}

// Resolve interprets args (0, 1, or 2 elements) as a rendering target,
// following go doc's parseArgs algorithm.
func Resolve(args []string) (*Target, error) {
	return parseArgs(args)
}

// LoadDir resolves the package at the given filesystem directory,
// bypassing import-path resolution. It is intended for callers (such
// as the TUI) that already know the package's location on disk and
// want to avoid the cost of consulting the module graph.
func LoadDir(dir string) (*Target, error) {
	bpkg, err := build.Default.ImportDir(dir, build.ImportComment)
	if err != nil {
		return nil, err
	}
	return &Target{Build: bpkg}, nil
}

// parseArgs is a port of cmd/go/internal/doc.parseArgs. It returns the
// first target found by walking the same sequence of fallbacks go doc
// itself uses.
func parseArgs(args []string) (*Target, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return fromDir(wd, "", "")
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
			return &Target{Build: bpkg, UserPath: args[0], Symbol: sym, Method: method}, nil
		}
		if t, err := fuzzy(arg, sym, method); err == nil {
			t.UserPath = args[0]
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
			return &Target{Build: bpkg, UserPath: arg}, nil
		}
		importErr = err
	} else {
		bpkg, err := build.Default.Import(arg, wd, build.ImportComment)
		if err == nil {
			return &Target{Build: bpkg, UserPath: arg}, nil
		}
		importErr = err
	}

	// If arg starts with an upper-case letter and has no slashes, it
	// can only be a symbol in the current directory. This kills the
	// problem of case-insensitive filesystems matching an upper-case
	// name as a package name.
	if !strings.ContainsAny(arg, `/\`) && token.IsExported(arg) {
		sym, method := splitSym(arg)
		if t, err := fromDir(".", sym, method); err == nil {
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
			return &Target{Build: bpkg, UserPath: pkgPath, Symbol: sym, Method: method}, nil
		}
		if t, err := fuzzy(pkgPath, sym, method); err == nil {
			t.UserPath = pkgPath
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
	return fromDir(".", sym, method)
}

// fromDir locates the package rooted at dir and returns a Target.
// The directory is resolved to an absolute path before lookup so
// go/build can recognize it as GOROOT/src or a module member and set
// the package's ImportPath accordingly.
func fromDir(dir, sym, method string) (*Target, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	bpkg, err := build.Default.ImportDir(abs, build.ImportComment)
	if err != nil {
		return nil, err
	}
	return &Target{Build: bpkg, Symbol: sym, Method: method}, nil
}

// fuzzy searches indexed package directories for one whose import path
// is or ends with name, matching go doc's findNextPackage: the standard
// library and current module are tried first, then the current module's
// dependencies. It returns the first candidate that imports cleanly; an
// upper-case name returns no match because it cannot be a package name.
func fuzzy(name, sym, method string) (*Target, error) {
	if name == "" || token.IsExported(name) {
		return nil, fmt.Errorf("no package matching %q", name)
	}
	for _, e := range modindex.Default().FindPackage(name) {
		bpkg, err := build.Default.ImportDir(e.Dir, build.ImportComment)
		if err != nil {
			continue
		}
		if bpkg.ImportPath == "" || bpkg.ImportPath == "." {
			bpkg.ImportPath = e.ImportPath
		}
		return &Target{Build: bpkg, Symbol: sym, Method: method}, nil
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
