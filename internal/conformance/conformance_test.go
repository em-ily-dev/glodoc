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

func TestGoDocParity(t *testing.T) {
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			goDoc, goDocOK := runTool(t, "go", append([]string{"doc"}, tc.args...)...)
			glodoc, glodocOK := runTool(t, glodocBin, tc.args...)

			var faults []string
			if goDocOK != glodocOK {
				faults = append(faults, fmt.Sprintf("exit status differs: go doc ok=%v, glodoc ok=%v", goDocOK, glodocOK))
			}
			if goDoc != glodoc {
				faults = append(faults, "output differs from go doc\n"+firstDiff(goDoc, glodoc))
			}
			for _, pat := range tc.yes {
				if !regexp.MustCompile(pat).MatchString(glodoc) {
					faults = append(faults, fmt.Sprintf("no match for %q", pat))
				}
			}
			for _, pat := range tc.no {
				if regexp.MustCompile(pat).MatchString(glodoc) {
					faults = append(faults, fmt.Sprintf("unwanted match for %q", pat))
				}
			}

			switch {
			case tc.diff == "":
				for _, f := range faults {
					t.Error(f)
				}
				if t.Failed() {
					t.Logf("normalized glodoc output:\n%s", glodoc)
				}
			case len(faults) == 0:
				t.Errorf("case passes in full; known divergence appears resolved (%s) — remove the diff annotation", tc.diff)
			default:
				t.Logf("known divergence (%s); %d failing check(s), first: %s", tc.diff, len(faults), faults[0])
			}
		})
	}
}

// runTool executes a command and returns its normalized stdout and
// whether it exited successfully. Stderr is reported through the test
// log on failure paths but does not participate in comparison yet;
// error-message parity is tracked as future work.
func runTool(t *testing.T, bin string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && stderr.Len() > 0 {
		t.Logf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return normalize(stdout.String()), err == nil
}

var (
	oscEscape  = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	csiEscape  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	whitespace = regexp.MustCompile(`\s+`)
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
