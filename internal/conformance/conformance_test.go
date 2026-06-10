// Package conformance verifies that glodoc's output matches "go doc".
//
// glodoc is intended to match go doc exactly in every relevant way; the
// only deliberate differences are colorization and the no-argument TUI.
// The tests here exec both tools with identical arguments against the
// same corpus — a copy of go doc's own testdata package — and compare
// their output after a normalization that erases the deliberate
// differences: ANSI escape sequences are stripped and every whitespace
// run collapses to a single space, so color, wrapping, and indentation
// never affect the comparison, while any difference in content does.
// Standard error is compared the same way, after additionally dropping
// each tool's program-name prefix; exit status must agree as well.
//
// Each case may also carry regular expressions, adapted from go doc's
// own test suite, that the normalized glodoc output must (or must not)
// match. These pin specific behaviors — symbol selection, flag
// handling, elision of unexported details — independently of the
// whole-output comparison.
//
// A case with a non-empty diff field documents a known divergence from
// go doc: its checks are reported but do not fail the suite, and the
// case instead asserts that at least one check still fails. The ledger
// stays honest both ways — fixing a divergence flips its case loudly,
// prompting removal of the annotation and restoring full enforcement.
//
// The corpus is addressed as "./testdata" rather than by import path
// because the go tool refuses to resolve import paths containing a
// testdata element; both tools resolve the relative form through the
// same directory-based lookup.
package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// p is the path both tools are given to reach the conformance corpus,
// a copy of the testdata package from cmd/go/internal/doc.
const p = "./testdata"

// pQuoted is the corpus's import path as printed in package clauses,
// quoted for use inside table regexps. It differs from p because both
// tools resolve the relative argument to the canonical import path.
var pQuoted = regexp.QuoteMeta("ily.dev/glodoc/internal/conformance/testdata")

// glodocBin is the glodoc binary built once by TestMain.
var glodocBin string

// test describes one parity case: arguments passed identically to both
// tools, regexps the normalized glodoc output must and must not match,
// and an optional known-divergence annotation.
type test struct {
	name string
	args []string // Arguments to both "glodoc" and "go doc".
	dir  string   // Working directory; empty means the package directory.
	yes  []string // Regexps that must match the normalized output.
	no   []string // Regexps that must not match.
	diff string   // If non-empty, why the outputs are known to differ.
}

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

// testMain builds the glodoc binary into a temporary directory and
// runs the tests against it. It exists so the deferred cleanup runs
// before the process exits.
func testMain(m *testing.M) int {
	dir, err := os.MkdirTemp("", "glodoc-conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	glodocBin = filepath.Join(dir, "glodoc")
	build := exec.Command("go", "build", "-o", glodocBin, "ily.dev/glodoc")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building glodoc:", err)
		return 1
	}
	return m.Run()
}

// TestGoDocParity runs the table adapted from go doc's own test suite.
func TestGoDocParity(t *testing.T) {
	runTable(t, tests)
}

// TestLookupParity runs the cases adapted from go doc's non-table
// tests, which exercise package lookup across the directory scan.
func TestLookupParity(t *testing.T) {
	runTable(t, lookupTests)
}

func runTable(t *testing.T, table []test) {
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			goDoc := runTool(t, tc.dir, "go", append([]string{"doc"}, tc.args...)...)
			glodoc := runTool(t, tc.dir, glodocBin, tc.args...)

			var faults []string
			if goDoc.ok != glodoc.ok {
				faults = append(faults, fmt.Sprintf("exit status differs: go doc ok=%v, glodoc ok=%v", goDoc.ok, glodoc.ok))
			}
			if goDoc.stdout != glodoc.stdout {
				faults = append(faults, "output differs from go doc\n"+firstDiff(goDoc.stdout, glodoc.stdout))
			}
			if goDoc.stderr != glodoc.stderr {
				faults = append(faults, fmt.Sprintf("stderr differs from go doc\ngo doc: %s\nglodoc: %s", goDoc.stderr, glodoc.stderr))
			}
			for _, pat := range tc.yes {
				if !regexp.MustCompile(pat).MatchString(glodoc.stdout) {
					faults = append(faults, fmt.Sprintf("no match for %q", pat))
				}
			}
			for _, pat := range tc.no {
				if regexp.MustCompile(pat).MatchString(glodoc.stdout) {
					faults = append(faults, fmt.Sprintf("unwanted match for %q", pat))
				}
			}

			switch {
			case tc.diff == "":
				for _, f := range faults {
					t.Error(f)
				}
				if t.Failed() {
					t.Logf("normalized glodoc output:\n%s", glodoc.stdout)
				}
			case len(faults) == 0:
				t.Errorf("case passes in full; known divergence appears resolved (%s) — remove the diff annotation", tc.diff)
			default:
				t.Logf("known divergence (%s); %d failing check(s), first: %s", tc.diff, len(faults), faults[0])
			}
		})
	}
}

// result holds one tool invocation's observable behavior, normalized
// for comparison.
type result struct {
	stdout string
	stderr string
	ok     bool
}

// runTool executes a command in dir and returns its normalized output
// and whether it exited successfully.
func runTool(t *testing.T, dir, bin string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return result{
		stdout: normalize(stdout.String()),
		stderr: normalizeStderr(stderr.String()),
		ok:     err == nil,
	}
}

var (
	oscEscape  = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	csiEscape  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	whitespace = regexp.MustCompile(`\s+`)
	// progPrefix is the program-name prefix each tool puts on its error
	// lines: "doc: " for go doc (its log prefix), "glodoc: " for glodoc.
	progPrefix = regexp.MustCompile(`(?m)^(doc|glodoc): `)
)

// normalize reduces tool output to its content: ANSI escape sequences
// are removed and every whitespace run becomes a single space. The
// rule is deliberately blunt — it cannot distinguish glodoc's chrome
// from go doc's, so it can never mask a content difference; it only
// forgives layout.
func normalize(s string) string {
	s = oscEscape.ReplaceAllString(s, "")
	s = csiEscape.ReplaceAllString(s, "")
	s = whitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// normalizeStderr normalizes like normalize and additionally drops the
// program-name prefix from each line, the one legitimate difference
// between the tools' error output.
func normalizeStderr(s string) string {
	return normalize(progPrefix.ReplaceAllString(s, ""))
}

// firstDiff renders a short excerpt of two strings centered on the
// first position where they diverge, so a failure points at the
// difference rather than dumping two pages of output.
func firstDiff(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	excerpt := func(s string) string {
		start := max(0, i-60)
		end := min(len(s), i+120)
		var pre, post string
		if start > 0 {
			pre = "…"
		}
		if end < len(s) {
			post = "…"
		}
		return pre + s[start:end] + post
	}
	return fmt.Sprintf("go doc: %s\nglodoc: %s", excerpt(a), excerpt(b))
}
