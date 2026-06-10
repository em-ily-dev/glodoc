// Package modindex enumerates the package directories visible to
// glodoc: the Go standard library under GOROOT, every directory of the
// current module that contains Go source, and the directories of the
// current module's dependencies.
//
// Entries appear in the order go doc scans its code roots — GOROOT/src,
// GOROOT/src/cmd, the current module, then dependencies — each walked
// breadth-first in lexical order, so a scan over the index selects the
// same package go doc's own directory scan would.
//
// The standard library and current module are indexed together on first
// use. The dependency directories, which require consulting the module
// graph, are indexed separately and only on first access; this keeps
// the common case—a lookup satisfied by the standard library or the
// current module—free of that cost. Both indexes are cached for the
// lifetime of the process.
package modindex

import (
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Source identifies where an entry came from.
type Source int

const (
	// SourceStdlib indicates a package under GOROOT/src.
	SourceStdlib Source = iota
	// SourceModule indicates a package in the current module's tree.
	SourceModule
	// SourceDependency indicates a package in one of the current
	// module's dependencies.
	SourceDependency
)

// Entry describes one indexed package directory.
type Entry struct {
	// ImportPath is the package's canonical import path.
	ImportPath string
	// Dir is the absolute filesystem path to the package directory.
	Dir string
	// Source identifies the origin of the entry.
	Source Source
}

// Index is a snapshot of known package directories.
type Index struct {
	once    sync.Once
	entries []Entry

	depRootsOnce sync.Once
	depRoots     []Root

	depOnce    sync.Once
	depEntries []Entry
}

// defaultIndex is the process-wide lazy index.
var defaultIndex = &Index{}

// Default returns the process-wide index, building it on first access.
func Default() *Index { return defaultIndex }

// All returns every eagerly indexed entry: the standard library and
// the current module, in go doc's scan order. Dependency entries are
// indexed separately; see Deps.
func (idx *Index) All() []Entry {
	idx.once.Do(idx.build)
	return idx.entries
}

// Deps returns the entries for the current module's dependencies,
// building them on first use. The build consults the module graph
// ("go list -m", or the vendor directory when the module vendors), so
// callers should exhaust All before consulting Deps; lookups satisfied
// by the standard library or current module then never pay that cost.
func (idx *Index) Deps() []Entry {
	idx.depOnce.Do(idx.buildDeps)
	return idx.depEntries
}

// Root is a code root: a directory tree of packages whose import
// paths are the root's import path extended with the path from Dir.
type Root struct {
	// Dir is the absolute filesystem path of the root.
	Dir string
	// ImportPath is the import path of the root itself; empty for
	// GOROOT/src, whose packages' import paths begin at its
	// subdirectories.
	ImportPath string
}

// Roots returns the eagerly known code roots used to derive canonical
// import paths from directories: GOROOT/src and the current module, in
// the order go doc consults its own code roots. Dependency module
// roots are kept separate so the common case never pays for the module
// graph; see DepRoots.
func Roots() []Root {
	var roots []Root
	if goroot := build.Default.GOROOT; goroot != "" {
		roots = append(roots, Root{Dir: filepath.Join(goroot, "src")})
	}
	if dir, mod, ok := findModule(); ok {
		roots = append(roots, Root{Dir: dir, ImportPath: mod})
	}
	return roots
}

// DepRoots returns the roots of the current module's dependencies,
// consulting the module graph on first use: each module in the build
// list with its location on disk, or the vendor directory (with an
// empty import path, as its subdirectories already spell out import
// paths) when the module vendors its dependencies.
func DepRoots() []Root {
	return defaultIndex.lazyDepRoots()
}

func (idx *Index) lazyDepRoots() []Root {
	idx.depRootsOnce.Do(func() {
		main, vendored := vendorEnabled()
		if vendored {
			if main != nil {
				idx.depRoots = []Root{{Dir: filepath.Join(main.Dir, "vendor")}}
			}
			return
		}
		out, err := exec.Command(goCmd(), "list", "-m", "-f", "{{.Path}}\t{{.Dir}}", "all").Output()
		if err != nil {
			return
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			modPath, dir, ok := strings.Cut(line, "\t")
			if !ok || dir == "" || (main != nil && modPath == main.Path) {
				continue
			}
			idx.depRoots = append(idx.depRoots, Root{Dir: dir, ImportPath: modPath})
		}
	})
	return idx.depRoots
}

// Module returns just the entries belonging to the current module.
func (idx *Index) Module() []Entry {
	var ms []Entry
	for _, e := range idx.All() {
		if e.Source == SourceModule {
			ms = append(ms, e)
		}
	}
	return ms
}

// build walks the eager code roots, populating entries in the order go
// doc scans its own: GOROOT/src (stopping at the cmd module boundary),
// then GOROOT/src/cmd, then the current module.
func (idx *Index) build() {
	if root := build.Default.GOROOT; root != "" {
		walk(filepath.Join(root, "src"), "", SourceStdlib, true, &idx.entries)
		walk(filepath.Join(root, "src", "cmd"), "cmd", SourceStdlib, true, &idx.entries)
	}
	if dir, mod, ok := findModule(); ok {
		walk(dir, mod, SourceModule, true, &idx.entries)
	}
}

// buildDeps walks the package directories of the current module's
// dependency roots, populating depEntries. The standard library and
// main module are not walked here; they are already indexed by build.
// Any failure to consult the module graph leaves the dependency index
// empty.
func (idx *Index) buildDeps() {
	for _, root := range idx.lazyDepRoots() {
		// The vendor root (empty import path) never holds nested
		// module boundaries; module roots are walked with them.
		walk(root.Dir, root.ImportPath, SourceDependency, root.ImportPath != "", &idx.depEntries)
	}
}

// module describes the main module as reported by "go list -m".
type module struct {
	Path, Dir, GoVersion string
}

// modFlagRegexp extracts the value of a -mod flag from GOFLAGS.
var modFlagRegexp = regexp.MustCompile(`-mod[ =](\w+)`)

// vendorEnabled reports whether the main module's dependencies should be
// read from its vendor directory rather than the module cache, returning
// the main module alongside. It mirrors go doc's vendorEnabled: an
// explicit -mod in GOFLAGS is honored, otherwise vendoring is inferred
// from the presence of a vendor directory under a module declaring go
// 1.14 or newer. A nil module means the main module could not be
// determined.
func vendorEnabled() (*module, bool) {
	main, go114, ok := mainModule()
	if !ok {
		return nil, false
	}
	out, _ := exec.Command(goCmd(), "env", "GOFLAGS").Output()
	if m := modFlagRegexp.FindStringSubmatch(string(out)); m != nil {
		return &main, m[1] == "vendor"
	}
	if !go114 {
		return &main, false
	}
	if fi, err := os.Stat(filepath.Join(main.Dir, "vendor")); err == nil && fi.IsDir() {
		return &main, goVersionAtLeast(main.GoVersion, 1, 14)
	}
	return &main, false
}

// mainModule reports the main module and whether the running toolchain
// supports go 1.14 vendoring semantics, or ok=false if no main module
// could be determined. It mirrors go doc's getMainModuleAnd114.
func mainModule() (m module, go114, ok bool) {
	const format = `{{.Path}}
{{.Dir}}
{{.GoVersion}}
{{range context.ReleaseTags}}{{if eq . "go1.14"}}{{.}}{{end}}{{end}}`
	out, err := exec.Command(goCmd(), "list", "-m", "-f", format).Output()
	if err != nil {
		return module{}, false, false
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 4 {
		return module{}, false, false
	}
	return module{Path: lines[0], Dir: lines[1], GoVersion: lines[2]}, lines[3] == "go1.14", true
}

// goVersionAtLeast reports whether a go directive such as "1.21" or
// "1.14.3" is at least major.minor. An empty or unparseable version is
// treated as older, matching go doc's use of semver.Compare.
func goVersionAtLeast(v string, major, minor int) bool {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return maj > major || (maj == major && min >= minor)
}

// goCmd returns the path to the go command belonging to the GOROOT that
// go/build is configured to use, falling back to a bare "go" resolved
// from PATH.
func goCmd() string {
	if root := build.Default.GOROOT; root != "" {
		return filepath.Join(root, "bin", "go")
	}
	return "go"
}

// walk visits root in breadth-first lexical order, appending an entry
// for each directory that contains buildable Go source. The order
// matches go doc's directory scan, where the package chosen for a
// partial path is the matching one nearest the root and lexically
// first at its level. When boundary is set, the walk stops at nested
// module boundaries: a subdirectory holding its own go.mod belongs to
// a different module and is pruned along with its subtree, so its
// packages are not attributed to root's module.
func walk(root, importPrefix string, src Source, boundary bool, out *[]Entry) {
	this := []string{}
	next := []string{root}
	for len(next) > 0 {
		this, next = next, this[:0]
		for _, dir := range this {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			hasGo := false
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() {
					if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
						hasGo = true
					}
					continue
				}
				if skipDir(name) {
					continue
				}
				sub := filepath.Join(dir, name)
				if boundary && hasGoMod(sub) {
					continue
				}
				next = append(next, sub)
			}
			if !hasGo {
				continue
			}
			ip := importPrefix
			if dir != root {
				rel := filepath.ToSlash(dir[len(root)+1:])
				if ip == "" {
					ip = rel
				} else {
					ip = ip + "/" + rel
				}
			}
			*out = append(*out, Entry{ImportPath: ip, Dir: dir, Source: src})
		}
	}
}

// skipDir reports whether a directory name should be pruned from the
// walk. We skip the conventional opt-outs (testdata, vendor, dotted,
// underscored).
func skipDir(name string) bool {
	switch {
	case name == "testdata", name == "vendor":
		return true
	case strings.HasPrefix(name, "."):
		return true
	case strings.HasPrefix(name, "_"):
		return true
	}
	return false
}

// hasGoMod reports whether dir is the root of a module, i.e. holds a
// go.mod file.
func hasGoMod(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil && !fi.IsDir()
}

// findModule walks upward from the current working directory looking
// for a go.mod file and returns the module's root directory and import
// path.
func findModule() (root, modPath string, ok bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", false
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			if mp := modulePath(data); mp != "" {
				return dir, mp, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

// modulePath extracts the module path from go.mod source.
func modulePath(data []byte) string {
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), "\"")
		}
	}
	return ""
}
