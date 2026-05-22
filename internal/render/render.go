// Package render converts parsed Go documentation into markdown.
//
// The markdown produced mirrors the structure of "go doc -all": a package
// synopsis followed by sections for constants, variables, functions, and
// types, with each type carrying its constructors and methods. Examples are
// emitted inline beneath the symbol they exercise.
//
// The output is intended to be fed to a CommonMark renderer such as
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

// Package renders pkg as markdown.
//
// When sym is empty, the full package is rendered. When sym is non-empty,
// only the matching top-level symbol (function, type, constant, or
// variable) is rendered; if method is also non-empty, the rendering is
// further narrowed to that method or field of sym.
//
// If the requested symbol or method does not exist, the returned string
// describes the miss in place of the missing content; an error is not
// reported because callers typically want to display something either way.
func Package(pkg *doc.Package, fset *token.FileSet, sym, method string) string {
	r := &renderer{
		pkg:    pkg,
		fset:   fset,
		parser: pkg.Parser(),
	}
	var b strings.Builder
	r.header(&b)
	if sym == "" {
		r.full(&b)
	} else {
		r.symbol(&b, sym, method)
	}
	return b.String()
}

type renderer struct {
	pkg    *doc.Package
	fset   *token.FileSet
	parser *comment.Parser
}

// header writes the package title and import path.
func (r *renderer) header(b *strings.Builder) {
	fmt.Fprintf(b, "# package %s\n\n", r.pkg.Name)
	fmt.Fprintf(b, "```go\nimport %q\n```\n\n", r.pkg.ImportPath)
}

// full writes the whole package: overview, constants, variables, functions,
// types, and notes.
func (r *renderer) full(b *strings.Builder) {
	if r.pkg.Doc != "" {
		b.WriteString(r.prose(r.pkg.Doc, 2))
		b.WriteString("\n")
	}
	r.examplesFor(b, r.pkg.Examples, 2)
	if len(r.pkg.Consts) > 0 {
		b.WriteString("## Constants\n\n")
		r.values(b, r.pkg.Consts)
	}
	if len(r.pkg.Vars) > 0 {
		b.WriteString("## Variables\n\n")
		r.values(b, r.pkg.Vars)
	}
	for _, f := range r.pkg.Funcs {
		r.function(b, f, 2)
	}
	for _, t := range r.pkg.Types {
		r.typ(b, t)
	}
	r.notes(b)
}

// symbol writes a single top-level symbol, optionally narrowed to a method
// or field.
func (r *renderer) symbol(b *strings.Builder, sym, method string) {
	for _, c := range r.pkg.Consts {
		if hasName(c.Names, sym) {
			r.values(b, []*doc.Value{c})
			return
		}
	}
	for _, v := range r.pkg.Vars {
		if hasName(v.Names, sym) {
			r.values(b, []*doc.Value{v})
			return
		}
	}
	for _, f := range r.pkg.Funcs {
		if f.Name == sym {
			r.function(b, f, 2)
			return
		}
	}
	for _, t := range r.pkg.Types {
		if t.Name != sym {
			continue
		}
		if method == "" {
			r.typ(b, t)
			return
		}
		for _, m := range t.Methods {
			if m.Name == method {
				r.function(b, m, 2)
				return
			}
		}
		if f := findField(t.Decl, method); f != nil {
			r.field(b, t, method, f)
			return
		}
		fmt.Fprintf(b, "_no method or field %s.%s_\n", sym, method)
		return
	}
	fmt.Fprintf(b, "_no symbol named %s_\n", sym)
}

// values renders a group of constants or variables.
func (r *renderer) values(b *strings.Builder, vs []*doc.Value) {
	for _, v := range vs {
		b.WriteString(codeBlock(r.decl(v.Decl)))
		b.WriteString("\n")
		if v.Doc != "" {
			b.WriteString(r.prose(v.Doc, 4))
			b.WriteString("\n")
		}
	}
}

// function renders a free function or method.
//
// headingLevel is the markdown heading level used for the symbol name.
func (r *renderer) function(b *strings.Builder, f *doc.Func, headingLevel int) {
	fmt.Fprintf(b, "%s func %s\n\n", strings.Repeat("#", headingLevel), funcHeading(f))
	b.WriteString(codeBlock(r.decl(f.Decl)))
	b.WriteString("\n")
	if f.Doc != "" {
		b.WriteString(r.prose(f.Doc, headingLevel+1))
		b.WriteString("\n")
	}
	r.examplesFor(b, f.Examples, headingLevel+1)
}

// typ renders a type and its attached constructors and methods.
func (r *renderer) typ(b *strings.Builder, t *doc.Type) {
	fmt.Fprintf(b, "## type %s\n\n", t.Name)
	b.WriteString(codeBlock(r.decl(t.Decl)))
	b.WriteString("\n")
	if t.Doc != "" {
		b.WriteString(r.prose(t.Doc, 3))
		b.WriteString("\n")
	}
	r.examplesFor(b, t.Examples, 3)
	if len(t.Consts) > 0 {
		r.values(b, t.Consts)
	}
	if len(t.Vars) > 0 {
		r.values(b, t.Vars)
	}
	for _, f := range t.Funcs {
		r.function(b, f, 3)
	}
	for _, m := range t.Methods {
		r.function(b, m, 3)
	}
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

// exampleCode renders an example's body as Go source. Examples are
// stored as the body block of an ExampleXxx function; we unwrap the
// outer braces and dedent so the code reads naturally.
func (r *renderer) exampleCode(ex *doc.Example) string {
	if ex.Play != nil {
		// Use the synthesized playable form when available: it includes
		// any necessary imports and a clean package declaration.
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

// dedent removes one level of leading tab indentation from each line,
// which is what example bodies inherit from being inside a function.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\t")
	}
	return strings.Join(lines, "\n")
}

// prose converts a godoc comment string into markdown, with headings
// inside the comment starting at the given level.
func (r *renderer) prose(text string, headingLevel int) string {
	p := &comment.Printer{HeadingLevel: headingLevel}
	return string(p.Markdown(r.parser.Parse(text)))
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

// hasName reports whether name appears in names.
func hasName(names []string, name string) bool {
	return slices.Contains(names, name)
}

// findField looks up a field by name in a struct type declaration.
func findField(decl *ast.GenDecl, name string) *ast.Field {
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
				if fn.Name == name {
					return f
				}
			}
		}
	}
	return nil
}
