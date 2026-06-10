package conformance

import (
	"go/build"
	"path/filepath"
)

// lookupTests ports the non-table tests from go doc's test suite —
// TestMultiplePackages, TestTwoArgLookup, TestDotSlashLookup, and
// TestNoPackageClauseWhenNoMatch — which exercise package lookup
// across the directory scan rather than rendering. Upstream asserts
// specific error messages; here the stderr and exit-status comparisons
// in the shared runner cover the same ground.
//
// These cases scan GOROOT (and, on misses, the module graph), so they
// are slower than the corpus-bound table.
var lookupTests = []test{
	// TestMultiplePackages: a partial package path must be scanned past
	// packages that lack the symbol — crypto/rand precedes math/rand in
	// the scan, but only the latter has Float64 — and a symbol found
	// nowhere must report every package the scan tried.
	{
		name: "multiple packages: rand.Float64",
		args: []string{"rand.Float64"},
		yes:  []string{`^package rand // import "math/rand"`, `func Float64\(\) float64`},
	},
	{
		// go doc is built with -trimpath, which leaves its internal
		// go/build context without a GOROOT, so its whole-argument
		// import fails and it reports that import failure (mentioning
		// "$GOROOT not set" and GOPATH probe paths). glodoc's import
		// resolves crypto/rand and reports the missing symbol instead.
		// Replicating an artifact of upstream's build configuration
		// would make glodoc's error strictly less truthful; ledgered as
		// a deliberate divergence.
		name: "multiple packages: crypto/rand.float64",
		args: []string{"crypto/rand.float64"},
		diff: "error text: go doc reports its GOROOT-less import failure, glodoc the missing symbol",
	},
	{
		name: "multiple packages: rand.doesnotexit",
		args: []string{"rand.doesnotexit"},
	},
	{
		name: "multiple packages: rand.Intn",
		args: []string{"rand.Intn"},
		yes:  []string{`^package rand // import "math/rand"`, `func Intn\(n int\) int`},
	},

	// TestTwoArgLookup: the two-argument form scans for the package the
	// same way, and reports misses per package.
	{
		name: "two-arg: binary BigEndian",
		args: []string{"binary", "BigEndian"},
		yes:  []string{`^package binary // import "encoding/binary"`},
	},
	{
		name: "two-arg: rand Float64",
		args: []string{"rand", "Float64"},
	},
	{
		name: "two-arg: bytes Foo",
		args: []string{"bytes", "Foo"},
	},
	{
		name: "two-arg: nosuchpackage Foo",
		args: []string{"nosuchpackage", "Foo"},
	},

	// TestNoPackageClauseWhenNoMatch: a failed lookup must not print
	// the package clause.
	{
		name: "no package clause when no match",
		args: []string{"template.ZZZ"},
		no:   []string{`package template`},
	},

	// TestDotSlashLookup: a dot-slash path resolves relative to the
	// working directory and prints the canonical import path.
	{
		name: "dot-slash lookup",
		args: []string{"./template"},
		dir:  filepath.Join(build.Default.GOROOT, "src", "text"),
		yes:  []string{`^package template // import "text/template"`},
	},
}
