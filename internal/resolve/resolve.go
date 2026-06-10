// Package resolve interprets glodoc command-line arguments as a
// documentation rendering target, following the algorithm used by
// "go doc".
//
// The argument grammar mirrors go doc: zero arguments resolves to the
// current directory; one argument is tried as a complete package path,
// then (if the first letter is upper case) as a symbol in the current
// directory, then progressively shorter "<pkg>.<sym>" splits, scanning
// known package directories for a trailing-path-segment match at each
// step; two arguments are a package followed by "<sym>[.<methodOrField>]".
//
// A Resolver is stateful, mirroring go doc's directory scanner: when a
// resolved package turns out not to contain the requested symbol, the
// caller resolves again and receives the next candidate, continuing the
// scan where it left off. The more result reports whether such a retry
// can yield anything. The scan order is the standard library, then the
// current module, then the module's dependencies — the dependency tier
// is consulted lazily, so lookups satisfied earlier never pay for the
// module graph.
//
// Resolution stops at locating a package: the result identifies a
// directory of Go source and the symbol selectors, and parsing is left
// to the renderer, mirroring the seam between go doc's parseArgs and
// parsePackage.
package resolve

import (
	"fmt"
	"go/build"
	"go/token"
	"os"
	"path"
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

// Resolver resolves arguments to successive candidate packages. The
// zero value is ready to use.
type Resolver struct {
	scan dirScan
}

// Resolve interprets args (0, 1, or 2 elements) as a rendering target,
// a port of go doc's parseArgs. Successive calls continue the
// directory scan, returning the next candidate package for the same
// arguments. The more result reports whether resolving again may find
// another candidate; it is meaningful only to callers that did not
// find what they wanted in the returned target.
func (r *Resolver) Resolve(args []string) (t *Target, more bool, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, false, err
	}
	if len(args) == 0 {
		t, err := fromDir(wd, "", "")
		return t, false, err
	}

	arg := args[0]
	// Convert a leading "./" or "../" to an absolute path so we don't
	// confuse a local "./errors" with the standard "errors" package.
	if isDotSlash(arg) {
		arg = filepath.Join(wd, arg)
	}

	switch len(args) {
	case 2:
		sym, method, err := splitSym(args[1])
		if err != nil {
			return nil, false, err
		}
		if bpkg, err := build.Default.Import(args[0], wd, build.ImportComment); err == nil {
			return &Target{Build: bpkg, UserPath: args[0], Symbol: sym, Method: method}, false, nil
		}
		for {
			e, ok := r.findNextPackage(arg)
			if !ok {
				break
			}
			if bpkg, err := build.Default.ImportDir(e.Dir, build.ImportComment); err == nil {
				return &Target{Build: bpkg, UserPath: arg, Symbol: sym, Method: method}, true, nil
			}
		}
		return nil, false, fmt.Errorf("no such package: %s", args[0])
	case 1:
		// fall through to the one-arg logic below
	default:
		return nil, false, fmt.Errorf("expected at most 2 arguments, got %d", len(args))
	}

	// One-arg case. Try the arg as a complete package path first;
	// this short-circuits the period splits below so a package path
	// that contains another package path as a prefix can't be
	// confused for "<prefix>.<rest>".
	var importErr error
	if filepath.IsAbs(arg) {
		bpkg, err := build.Default.ImportDir(arg, build.ImportComment)
		if err == nil {
			return &Target{Build: bpkg, UserPath: arg}, false, nil
		}
		importErr = err
	} else {
		bpkg, err := build.Default.Import(arg, wd, build.ImportComment)
		if err == nil {
			return &Target{Build: bpkg, UserPath: arg}, false, nil
		}
		importErr = err
	}

	// If arg starts with an upper-case letter and has no slashes, it
	// can only be a symbol in the current directory. This kills the
	// problem of case-insensitive filesystems matching an upper-case
	// name as a package name.
	if !strings.ContainsAny(arg, `/\`) && token.IsExported(arg) {
		sym, method, err := splitSym(arg)
		if err == nil {
			if t, err := fromDir(".", sym, method); err == nil {
				return t, false, nil
			}
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
		sym, method, err := splitSym(rest)
		if err != nil {
			return nil, false, err
		}
		if bpkg, err := build.Default.Import(pkgPath, wd, build.ImportComment); err == nil {
			return &Target{Build: bpkg, UserPath: pkgPath, Symbol: sym, Method: method}, false, nil
		}
		// See if we have the basename or tail of a package, as in json
		// for encoding/json or ivy/value for robpike.io/ivy/value.
		for {
			e, ok := r.findNextPackage(pkgPath)
			if !ok {
				break
			}
			if bpkg, err := build.Default.ImportDir(e.Dir, build.ImportComment); err == nil {
				return &Target{Build: bpkg, UserPath: pkgPath, Symbol: sym, Method: method}, true, nil
			}
		}
		// The next iteration of the loop must scan all the directories again.
		r.scan.Reset()
	}

	// If the original arg had a slash, no match is fatal: it can only
	// have been a package path.
	if slash >= 0 {
		return nil, false, importErr
	}

	// Last resort: assume a symbol in the current directory.
	sym, method, err := splitSym(arg)
	if err != nil {
		return nil, false, err
	}
	t, err = fromDir(".", sym, method)
	return t, false, err
}

// findNextPackage returns the next indexed package whose import path
// matches the (perhaps partial) package path pkg, continuing from
// wherever the previous call left off. It is a port of go doc's
// findNextPackage; an absolute path names its own directory and is
// yielded exactly once.
func (r *Resolver) findNextPackage(pkg string) (modindex.Entry, bool) {
	if filepath.IsAbs(pkg) {
		if r.scan.offset == 0 {
			r.scan.offset = -1
			return modindex.Entry{Dir: pkg}, true
		}
		return modindex.Entry{}, false
	}
	if pkg == "" || token.IsExported(pkg) { // Upper case symbol cannot be a package name.
		return modindex.Entry{}, false
	}
	pkg = path.Clean(pkg)
	pkgSuffix := "/" + pkg
	for {
		e, ok := r.scan.Next()
		if !ok {
			return modindex.Entry{}, false
		}
		if e.ImportPath == pkg || strings.HasSuffix(e.ImportPath, pkgSuffix) {
			return e, true
		}
	}
}

// dirScan is a resumable cursor over the indexed package directories:
// the eagerly indexed standard library and current module first, then
// the lazily indexed dependencies, which are not touched until the
// eager entries are exhausted.
type dirScan struct {
	offset int
}

// Next returns the next directory in the scan. The boolean is false
// when the scan is done.
func (d *dirScan) Next() (modindex.Entry, bool) {
	all := modindex.Default().All()
	if d.offset < len(all) {
		e := all[d.offset]
		d.offset++
		return e, true
	}
	deps := modindex.Default().Deps()
	if i := d.offset - len(all); i < len(deps) {
		d.offset++
		return deps[i], true
	}
	return modindex.Entry{}, false
}

// Reset puts the scan back at the beginning.
func (d *dirScan) Reset() { d.offset = 0 }

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

// splitSym splits "sym" or "sym.method" into its parts, mirroring go
// doc's parseSymbol, including its rejection of deeper selectors.
func splitSym(s string) (sym, method string, err error) {
	if s == "" {
		return "", "", nil
	}
	elem := strings.Split(s, ".")
	switch len(elem) {
	case 1:
	case 2:
		method = elem[1]
	default:
		return "", "", fmt.Errorf("too many periods in symbol specification")
	}
	return elem[0], method, nil
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
