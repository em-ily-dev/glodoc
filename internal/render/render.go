// Package render converts parsed Go documentation into markdown.
//
// The output mirrors "go doc": a default compact view that shows
// signatures with the package overview, an -all view that includes
// every method, example, and note, a -short listing of one symbol per
// line, and a -src view that emits the source code of the matched
// declaration.
//
// The markdown is intended to be fed to a CommonMark renderer such as
// Glamour; doc-comment prose is converted via go/doc/comment's Markdown
// printer, and Go declarations are rendered as fenced code blocks.
package render

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/doc/comment"
	"go/printer"
	"go/token"
	"slices"
	"strings"
)

// Options controls the structure and verbosity of rendered output.
type Options struct {
	// All includes every symbol with its full documentation, methods,
	// constructors, examples, and notes. Corresponds to "go doc -all".
	All bool
	// Short emits one line per symbol with no doc bodies and no
	// package overview. Corresponds to "go doc -short".
	Short bool
	// Src renders the matched declaration as Go source with its body
	// (and surrounding doc comments) preserved. Corresponds to
	// "go doc -src".
	Src bool
	// Unexported renders unexported symbols and fields. The renderer
	// also consults this option when applying go doc's case-folding
	// match rule. Corresponds to "go doc -u".
	Unexported bool
	// CaseSensitive requires exact-case matching when looking up
	// symbols. Otherwise lowercase characters in the query match any
	// case while uppercase characters match exactly, following the
	// matching rule used by go doc. Corresponds to "go doc -c".
	CaseSensitive bool
	// IncludeMain renders the contents of a package main; without it,
	// only the package overview is shown for main packages.
	// Corresponds to "go doc -cmd".
	IncludeMain bool
}

// Package renders pkg as markdown subject to opts.
//
// When sym is empty, the full package is rendered. When sym is
// non-empty, only the matching top-level symbol is rendered; if method
// is also non-empty, the rendering is further narrowed to that method
// or field of sym.
//
// If the requested symbol or method does not exist, the returned string
// describes the miss in place of the missing content.
func Package(pkg *doc.Package, fset *token.FileSet, sym, method string, opts Options) string {
	r := &renderer{
		pkg:    pkg,
		fset:   fset,
		opts:   opts,
		parser: pkg.Parser(),
	}
	var b strings.Builder
	if !opts.Short {
		r.header(&b)
	}
	if sym == "" {
		r.renderPackage(&b)
	} else {
		r.renderSymbol(&b, sym, method)
	}
	return b.String()
}

type renderer struct {
	pkg    *doc.Package
	fset   *token.FileSet
	opts   Options
	parser *comment.Parser
}

// header writes the package title and import path.
func (r *renderer) header(b *strings.Builder) {
	fmt.Fprintf(b, "# package %s\n\n", r.pkg.Name)
	fmt.Fprintf(b, "```go\nimport %q\n```\n\n", r.pkg.ImportPath)
}

// renderPackage writes the package-level view selected by opts.
func (r *renderer) renderPackage(b *strings.Builder) {
	switch {
	case r.opts.Short:
		r.shortListing(b)
	case r.opts.All:
		r.allPackage(b)
	default:
		r.defaultPackage(b)
	}
}

// defaultPackage matches the layout of "go doc <pkg>": the package
// overview followed by a single line per top-level symbol. Constants
// and variables grouped under a type are indented beneath it along
// with that type's constructors; free functions appear at the top
// level. No per-symbol doc comments or examples are emitted.
func (r *renderer) defaultPackage(b *strings.Builder) {
	if r.pkg.Doc != "" {
		b.WriteString(r.prose(r.pkg.Doc, 2))
		b.WriteString("\n")
	}
	if r.pkg.Name == "main" && !r.opts.IncludeMain {
		return
	}
	r.summaries(b)
	r.bugs(b)
}

// summaries writes the one-line summaries for every top-level symbol,
// matching the format produced by go doc's valueSummary, funcSummary,
// and typeSummary in its default (non-all, non-short) branch.
func (r *renderer) summaries(b *strings.Builder) {
	grouped := r.valuesGroupedByType()
	constructors := r.typeConstructors()

	b.WriteString("```go\n")
	for _, c := range r.pkg.Consts {
		if !grouped[c] {
			if line := r.valueOneLine(c.Decl); line != "" {
				b.WriteString(line + "\n")
			}
		}
	}
	for _, v := range r.pkg.Vars {
		if !grouped[v] {
			if line := r.valueOneLine(v.Decl); line != "" {
				b.WriteString(line + "\n")
			}
		}
	}
	for _, f := range r.pkg.Funcs {
		if !constructors[f] && r.symbolVisible(f.Name) {
			b.WriteString(r.decl(f.Decl) + "\n")
		}
	}
	for _, t := range r.pkg.Types {
		if !r.symbolVisible(t.Name) {
			continue
		}
		b.WriteString(r.typeDecl(t.Decl, false) + "\n")
		for _, c := range t.Consts {
			if line := r.valueOneLine(c.Decl); line != "" {
				b.WriteString("    " + line + "\n")
			}
		}
		for _, v := range t.Vars {
			if line := r.valueOneLine(v.Decl); line != "" {
				b.WriteString("    " + line + "\n")
			}
		}
		for _, f := range t.Funcs {
			if r.symbolVisible(f.Name) {
				b.WriteString("    " + r.decl(f.Decl) + "\n")
			}
		}
	}
	b.WriteString("```\n")
}

// valuesGroupedByType returns the set of *doc.Value entries that are
// attached to a type (so the type summary emits them indented).
func (r *renderer) valuesGroupedByType() map[*doc.Value]bool {
	g := map[*doc.Value]bool{}
	for _, t := range r.pkg.Types {
		if !r.symbolVisible(t.Name) {
			continue
		}
		for _, c := range t.Consts {
			g[c] = true
		}
		for _, v := range t.Vars {
			g[v] = true
		}
	}
	return g
}

// typeConstructors returns the set of *doc.Func entries that are
// constructors for a type (printed by the type summary, suppressed
// from the free-function list).
func (r *renderer) typeConstructors() map[*doc.Func]bool {
	c := map[*doc.Func]bool{}
	for _, t := range r.pkg.Types {
		for _, f := range t.Funcs {
			c[f] = true
		}
	}
	return c
}

// symbolVisible reports whether a symbol with the given name should
// appear in summaries. It honors the Unexported option in the same way
// "go doc -u" does.
func (r *renderer) symbolVisible(name string) bool {
	return r.opts.Unexported || token.IsExported(name)
}

// valueOneLine renders a const/var GenDecl as a single line in the
// shape "tok Name[ type][ = value][ ...]", matching the output of
// go doc's oneLineNode for *ast.GenDecl. A trailing " ..." indicates
// that the block contains more specs than the one shown.
func (r *renderer) valueOneLine(decl *ast.GenDecl) string {
	if len(decl.Specs) == 0 {
		return ""
	}
	trailer := ""
	if len(decl.Specs) > 1 {
		trailer = " ..."
	}
	// In a const block, the type of one spec can carry over to the
	// next via iota; track it as we iterate so we can attribute it
	// to the first exported spec we choose to render.
	var typ string
	for i, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if vs.Type != nil {
			typ = " " + r.exprText(vs.Type)
		} else if len(vs.Values) > 0 {
			typ = ""
		}
		if !r.symbolVisible(vs.Names[0].Name) {
			continue
		}
		val := ""
		if i < len(vs.Values) && vs.Values[i] != nil {
			val = " = " + r.exprText(vs.Values[i])
		} else if len(vs.Values) > 0 && vs.Values[0] != nil {
			val = " = " + r.exprText(vs.Values[0])
		}
		return fmt.Sprintf("%s %s%s%s%s", decl.Tok, vs.Names[0], typ, val, trailer)
	}
	return ""
}

// bugs prints package-level BUG notes after the summary, matching the
// trailing section produced by "go doc <pkg>".
func (r *renderer) bugs(b *strings.Builder) {
	notes := r.pkg.Notes["BUG"]
	if len(notes) == 0 {
		return
	}
	b.WriteString("\n## Bugs\n\n")
	for _, n := range notes {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(n.Body))
		b.WriteString("\n")
	}
}

// allPackage matches "go doc -all <pkg>": every symbol expanded with
// methods, examples, and notes.
func (r *renderer) allPackage(b *strings.Builder) {
	if r.pkg.Doc != "" {
		b.WriteString(r.prose(r.pkg.Doc, 2))
		b.WriteString("\n")
	}
	r.examplesFor(b, r.pkg.Examples, 2)
	if r.pkg.Name == "main" && !r.opts.IncludeMain {
		return
	}
	if len(r.pkg.Consts) > 0 {
		b.WriteString("## Constants\n\n")
		r.values(b, r.pkg.Consts, true)
	}
	if len(r.pkg.Vars) > 0 {
		b.WriteString("## Variables\n\n")
		r.values(b, r.pkg.Vars, true)
	}
	for _, f := range r.pkg.Funcs {
		r.functionFull(b, f, 2)
	}
	for _, t := range r.pkg.Types {
		r.typeFull(b, t)
	}
	r.notes(b)
}

// shortListing writes one line per top-level symbol, matching the
// shape of "go doc -short": free functions at the top level, types
// with a collapsed body, and a type's constructors and methods
// indented beneath it.
func (r *renderer) shortListing(b *strings.Builder) {
	b.WriteString("```go\n")
	for _, c := range r.pkg.Consts {
		fmt.Fprintln(b, r.decl(c.Decl))
	}
	for _, v := range r.pkg.Vars {
		fmt.Fprintln(b, r.decl(v.Decl))
	}
	for _, f := range r.pkg.Funcs {
		fmt.Fprintln(b, r.decl(f.Decl))
	}
	for _, t := range r.pkg.Types {
		fmt.Fprintln(b, r.typeDecl(t.Decl, false))
		for _, f := range t.Funcs {
			fmt.Fprintln(b, indent(r.decl(f.Decl), "    "))
		}
		for _, m := range t.Methods {
			fmt.Fprintln(b, indent(r.decl(m.Decl), "    "))
		}
	}
	b.WriteString("```\n")
}

// indent prefixes every non-empty line of s with prefix.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// renderSymbol writes a single top-level symbol, optionally narrowed
// to a method or field. It always shows the symbol with its full
// documentation; -all is implied when a specific symbol is requested.
func (r *renderer) renderSymbol(b *strings.Builder, sym, method string) {
	eq := r.eq()
	for _, c := range r.pkg.Consts {
		if slices.ContainsFunc(c.Names, eq(sym)) {
			r.values(b, []*doc.Value{c}, true)
			return
		}
	}
	for _, v := range r.pkg.Vars {
		if slices.ContainsFunc(v.Names, eq(sym)) {
			r.values(b, []*doc.Value{v}, true)
			return
		}
	}
	for _, f := range r.pkg.Funcs {
		if eq(sym)(f.Name) {
			if r.opts.Src {
				r.functionSrc(b, f)
			} else {
				r.functionFull(b, f, 2)
			}
			return
		}
	}
	// Constructors are grouped under their return type's Funcs; check
	// them too so "pkg.New" finds template.New, fmt.Errorf, etc.
	for _, t := range r.pkg.Types {
		for _, f := range t.Funcs {
			if eq(sym)(f.Name) {
				if r.opts.Src {
					r.functionSrc(b, f)
				} else {
					r.functionFull(b, f, 2)
				}
				return
			}
		}
	}
	for _, t := range r.pkg.Types {
		if !eq(sym)(t.Name) {
			continue
		}
		if method == "" {
			if r.opts.Src {
				r.typeSrc(b, t)
			} else {
				r.typeFull(b, t)
			}
			return
		}
		for _, m := range t.Methods {
			if eq(method)(m.Name) {
				if r.opts.Src {
					r.functionSrc(b, m)
				} else {
					r.functionFull(b, m, 2)
				}
				return
			}
		}
		if f := findField(t.Decl, method, r.opts.CaseSensitive); f != nil {
			r.field(b, t, method, f)
			return
		}
		fmt.Fprintf(b, "_no method or field %s.%s_\n", sym, method)
		return
	}
	fmt.Fprintf(b, "_no symbol named %s_\n", sym)
}

// eq returns a per-target name-equality predicate honoring
// CaseSensitive. The returned function is curried so it composes
// neatly with slices.ContainsFunc.
func (r *renderer) eq() func(target string) func(string) bool {
	if r.opts.CaseSensitive {
		return func(target string) func(string) bool {
			return func(s string) bool { return s == target }
		}
	}
	return func(target string) func(string) bool {
		return func(s string) bool { return strings.EqualFold(s, target) }
	}
}

// values renders a group of constants or variables. When withDoc is
// false the doc comment is omitted (used by the short listing path,
// which delegates to its own writer).
func (r *renderer) values(b *strings.Builder, vs []*doc.Value, withDoc bool) {
	for _, v := range vs {
		b.WriteString(codeBlock(r.decl(v.Decl)))
		b.WriteString("\n")
		if withDoc && v.Doc != "" {
			b.WriteString(r.prose(v.Doc, 4))
			b.WriteString("\n")
		}
	}
}

// functionFull renders a function in the all/symbol view: signature,
// doc comment, and any attached examples.
func (r *renderer) functionFull(b *strings.Builder, f *doc.Func, headingLevel int) {
	fmt.Fprintf(b, "%s func %s\n\n", strings.Repeat("#", headingLevel), funcHeading(f))
	b.WriteString(codeBlock(r.decl(f.Decl)))
	b.WriteString("\n")
	if f.Doc != "" {
		b.WriteString(r.prose(f.Doc, headingLevel+1))
		b.WriteString("\n")
	}
	r.examplesFor(b, f.Examples, headingLevel+1)
}

// functionSrc renders the source of a function, including its body
// and the doc comment carried on the AST, as a single Go code block.
func (r *renderer) functionSrc(b *strings.Builder, f *doc.Func) {
	fmt.Fprintf(b, "## func %s\n\n", funcHeading(f))
	b.WriteString(codeBlock(r.source(f.Decl)))
	b.WriteString("\n")
}

// typeFull renders a type with everything: full declaration, doc
// comment, examples, constructors, methods, and attached values.
func (r *renderer) typeFull(b *strings.Builder, t *doc.Type) {
	fmt.Fprintf(b, "## type %s\n\n", t.Name)
	b.WriteString(codeBlock(r.typeDecl(t.Decl, true)))
	b.WriteString("\n")
	if t.Doc != "" {
		b.WriteString(r.prose(t.Doc, 3))
		b.WriteString("\n")
	}
	r.examplesFor(b, t.Examples, 3)
	if len(t.Consts) > 0 {
		r.values(b, t.Consts, true)
	}
	if len(t.Vars) > 0 {
		r.values(b, t.Vars, true)
	}
	for _, f := range t.Funcs {
		r.functionFull(b, f, 3)
	}
	for _, m := range t.Methods {
		r.functionFull(b, m, 3)
	}
}

// typeSrc renders the source code of a type declaration.
func (r *renderer) typeSrc(b *strings.Builder, t *doc.Type) {
	fmt.Fprintf(b, "## type %s\n\n", t.Name)
	b.WriteString(codeBlock(r.source(t.Decl)))
	b.WriteString("\n")
}

// field renders a single struct field as the field signature plus its
// doc comment.
func (r *renderer) field(b *strings.Builder, t *doc.Type, name string, f *ast.Field) {
	fmt.Fprintf(b, "### %s.%s\n\n", t.Name, name)
	b.WriteString(codeBlock(r.decl(f)))
	b.WriteString("\n")
	if f.Doc != nil {
		b.WriteString(r.prose(f.Doc.Text(), 4))
		b.WriteString("\n")
	}
}

// notes renders BUG/TODO/etc. notes grouped by marker.
func (r *renderer) notes(b *strings.Builder) {
	if len(r.pkg.Notes) == 0 {
		return
	}
	for marker, ns := range r.pkg.Notes {
		fmt.Fprintf(b, "## %s\n\n", marker)
		for _, n := range ns {
			fmt.Fprintf(b, "- %s\n", strings.TrimSpace(n.Body))
		}
		b.WriteString("\n")
	}
}

// examplesFor renders the examples attached to a subject.
func (r *renderer) examplesFor(b *strings.Builder, exs []*doc.Example, headingLevel int) {
	for _, ex := range exs {
		title := "Example"
		if ex.Suffix != "" {
			title = "Example (" + ex.Suffix + ")"
		}
		fmt.Fprintf(b, "%s %s\n\n", strings.Repeat("#", headingLevel), title)
		if ex.Doc != "" {
			b.WriteString(r.prose(ex.Doc, headingLevel+1))
			b.WriteString("\n")
		}
		b.WriteString(codeBlock(r.exampleCode(ex)))
		b.WriteString("\n")
		if ex.Output != "" {
			b.WriteString("Output:\n\n")
			b.WriteString("```\n")
			b.WriteString(strings.TrimRight(ex.Output, "\n"))
			b.WriteString("\n```\n\n")
		}
	}
}

// exampleCode renders an example's body as Go source.
func (r *renderer) exampleCode(ex *doc.Example) string {
	if ex.Play != nil {
		return r.decl(ex.Play)
	}
	block, ok := ex.Code.(*ast.BlockStmt)
	if !ok {
		return r.decl(ex.Code)
	}
	var parts []string
	for _, stmt := range block.List {
		parts = append(parts, r.decl(stmt))
	}
	return dedent(strings.Join(parts, "\n"))
}

// dedent removes one level of leading tab indentation from each line.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\t")
	}
	return strings.Join(lines, "\n")
}

// prose converts a godoc comment string into markdown.
func (r *renderer) prose(text string, headingLevel int) string {
	p := &comment.Printer{
		HeadingLevel: headingLevel,
		DocLinkURL:   pkgGoDevURL,
	}
	return string(p.Markdown(r.parser.Parse(text)))
}

// pkgGoDevURL renders a parsed doc link as a pkg.go.dev URL.
func pkgGoDevURL(link *comment.DocLink) string {
	frag := link.Name
	if link.Recv != "" {
		frag = link.Recv + "." + link.Name
	}
	if link.ImportPath == "" {
		if frag == "" {
			return ""
		}
		return "#" + frag
	}
	base := "https://pkg.go.dev/" + link.ImportPath
	if frag == "" {
		return base
	}
	return base + "#" + frag
}

// decl pretty-prints a declaration node as Go source, stripping bodies
// and doc comments so the output is just the signature/specification.
func (r *renderer) decl(node ast.Node) string {
	switch x := node.(type) {
	case *ast.FuncDecl:
		c := *x
		c.Body = nil
		c.Doc = nil
		node = &c
	case *ast.GenDecl:
		c := *x
		c.Doc = nil
		node = &c
	}
	var buf bytes.Buffer
	cfg := &printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 4}
	_ = cfg.Fprint(&buf, r.fset, node)
	return buf.String()
}

// typeDecl renders a type declaration. When expand is false the body
// of a struct or interface literal is collapsed to "{ ... }", matching
// the compact format produced by "go doc <pkg>".
func (r *renderer) typeDecl(decl *ast.GenDecl, expand bool) string {
	if expand {
		return r.decl(decl)
	}
	var b strings.Builder
	first := true
	for _, spec := range decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		first = false
		b.WriteString("type ")
		b.WriteString(ts.Name.Name)
		if ts.TypeParams != nil {
			b.WriteString(r.exprText(ts.TypeParams))
		}
		if ts.Assign.IsValid() {
			b.WriteString(" = ")
		} else {
			b.WriteString(" ")
		}
		b.WriteString(r.oneLineType(ts.Type))
	}
	return b.String()
}

// oneLineType renders a type expression as a single line, collapsing
// struct and interface bodies to "{ ... }".
func (r *renderer) oneLineType(t ast.Expr) string {
	switch t.(type) {
	case *ast.StructType:
		return "struct{ ... }"
	case *ast.InterfaceType:
		return "interface{ ... }"
	}
	return r.exprText(t)
}

// exprText pretty-prints an AST expression node as Go source.
func (r *renderer) exprText(node ast.Node) string {
	var buf bytes.Buffer
	cfg := &printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 4}
	_ = cfg.Fprint(&buf, r.fset, node)
	return buf.String()
}

// source pretty-prints a declaration with its full body and any
// attached doc comments. It relies on the AST having been parsed
// (and preserved) with doc comments intact.
func (r *renderer) source(node ast.Node) string {
	var buf bytes.Buffer
	cfg := &printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 4}
	_ = cfg.Fprint(&buf, r.fset, node)
	return buf.String()
}

// codeBlock wraps s in a fenced ```go block.
func codeBlock(s string) string {
	return "```go\n" + strings.TrimRight(s, "\n") + "\n```\n"
}

// funcHeading renders the heading text for a function or method:
// either "Name" or "(Recv) Name".
func funcHeading(f *doc.Func) string {
	if f.Recv == "" {
		return f.Name
	}
	return "(" + f.Recv + ") " + f.Name
}

// findField looks up a field by name in a struct type declaration.
//
// caseSensitive controls whether matching is exact.
func findField(decl *ast.GenDecl, name string, caseSensitive bool) *ast.Field {
	match := func(s string) bool {
		if caseSensitive {
			return s == name
		}
		return strings.EqualFold(s, name)
	}
	for _, spec := range decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, f := range st.Fields.List {
			for _, fn := range f.Names {
				if match(fn.Name) {
					return f
				}
			}
		}
	}
	return nil
}
