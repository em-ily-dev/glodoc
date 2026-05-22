// Package modindex enumerates the package directories visible to
// glodoc: the Go standard library under GOROOT and every directory of
// the current module that contains Go source.
//
// The index is built lazily on first use and cached for the lifetime
// of the process. It exists primarily to support fuzzy lookup by final
// path segment and to enumerate the current module's packages for the
// TUI listing.
package modindex

import (
	"go/build"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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
}

// defaultIndex is the process-wide lazy index.
var defaultIndex = &Index{}

// Default returns the process-wide index, building it on first access.
func Default() *Index { return defaultIndex }

// All returns every indexed entry.
func (idx *Index) All() []Entry {
	idx.once.Do(idx.build)
	return idx.entries
}

// FindByBase returns entries whose import path ends in name (the final
// path segment matches). Stdlib hits are returned before module hits.
func (idx *Index) FindByBase(name string) []Entry {
	var matches []Entry
	for _, e := range idx.All() {
		if path.Base(e.ImportPath) == name {
			matches = append(matches, e)
		}
	}
	return matches
}

// FindBySuffix returns entries whose import path ends with suffix,
// matching whole path segments. This lets a query like "internal/foo"
// resolve to "ily.dev/glodoc/internal/foo" without ambiguity.
func (idx *Index) FindBySuffix(suffix string) []Entry {
	var matches []Entry
	for _, e := range idx.All() {
		if e.ImportPath == suffix ||
			strings.HasSuffix(e.ImportPath, "/"+suffix) {
			matches = append(matches, e)
		}
	}
	return matches
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

// build walks GOROOT/src and the current module, populating entries.
func (idx *Index) build() {
	if root := build.Default.GOROOT; root != "" {
		walk(filepath.Join(root, "src"), "", SourceStdlib, &idx.entries)
	}
	if dir, mod, ok := findModule(); ok {
		walk(dir, mod, SourceModule, &idx.entries)
	}
}

// walk recursively visits root, appending an entry for each directory
// that contains buildable Go source.
func walk(root, importPrefix string, src Source, out *[]Entry) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		base := d.Name()
		if p != root && skipDir(base) {
			return fs.SkipDir
		}
		if !hasGoFiles(p) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		ip := importPrefix
		if rel != "." {
			rel = filepath.ToSlash(rel)
			if ip == "" {
				ip = rel
			} else {
				ip = ip + "/" + rel
			}
		}
		*out = append(*out, Entry{ImportPath: ip, Dir: p, Source: src})
		return nil
	})
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

// hasGoFiles reports whether dir contains at least one non-test .go
// file. Test-only directories don't make sense as glodoc targets.
func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		return true
	}
	return false
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
