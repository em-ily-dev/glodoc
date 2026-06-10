// Package render formats Go package documentation as "go doc" does,
// with optional styling.
//
// The implementation is a faithful port of cmd/go/internal/doc/pkg.go
// (go1.26.3); functions keep their upstream names, order, and comments
// so the two files can be reviewed side by side, and the conformance
// suite (internal/conformance) verifies the output against go doc
// itself. The deliberate adaptations are:
//
//   - the command-line flags consulted as globals upstream are fields
//     of Options;
//   - output accumulates in a buffer returned by Render rather than
//     streaming to a writer, and hard exits become returned errors or
//     the recovered PackageError panic, as upstream's do does;
//   - a Style may colorize declarations, the package clause, and
//     section headers as they are emitted; every styled span is passed
//     without trailing newlines so the surrounding layout is preserved
//     and the text under the styling stays go doc's;
//   - the package clause's import-path canonicalization consults
//     package modindex instead of go doc's dirs scanner, and the
//     GOPATH-mode "package source is installed" warning is not ported.
package render

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"ily.dev/glodoc/internal/modindex"
)

const (
	punchedCardWidth = 80
	indent           = "    "
)

// Options carries the flag values that select and shape the output.
// Each field corresponds to the go doc flag named in its comment.
type Options struct {
	All           bool // -all: show all documentation for package
	Short         bool // -short: one-line representation for each symbol
	Src           bool // -src: show source code for symbol
	Unexported    bool // -u: show unexported symbols as well as exported
	CaseSensitive bool // -c: symbol matching honors case
	ShowCmd       bool // -cmd: show symbols even if package is a command

	// Style optionally colorizes the output; see Style.
	Style Style
}

// Style colorizes spans of rendered output. Each function receives the
// exact text go doc would print for that span, without trailing
// newlines, and returns it decorated; a nil function leaves its span
// unstyled. Styling must not change the text itself: the conformance
// suite compares output to go doc with escape sequences stripped.
type Style struct {
	// Clause styles the "package x // import y" clause.
	Clause func(string) string
	// Header styles the section headers printed by -all.
	Header func(string) string
	// Decl styles declarations: full declaration blocks as well as
	// one-line summaries.
	Decl func(string) string
}

func (s Style) clause(text string) string { return apply(s.Clause, text) }
func (s Style) header(text string) string { return apply(s.Header, text) }
func (s Style) decl(text string) string   { return apply(s.Decl, text) }

// apply invokes f on the text minus any trailing newlines, restoring
// them afterward, so stylers never have to reason about layout.
func apply(f func(string) string, text string) string {
	if f == nil {
		return text
	}
	body := strings.TrimRight(text, "\n")
	return f(body) + text[len(body):]
}

// Package holds a parsed package and renders documentation for it.
// It mirrors the Package type in cmd/go/internal/doc.
type Package struct {
	opts        Options
	name        string       // Package name, json for encoding/json.
	userPath    string       // String the user used to find this package.
	pkg         *ast.Package // Parsed package.
	file        *ast.File    // Merged from all files in the package
	doc         *doc.Package
	build       *build.Package
	typedValue  map[*doc.Value]bool // Consts and vars related to types.
	constructor map[*doc.Func]bool  // Constructors.
	fs          *token.FileSet      // Needed for printing.
	buf         pkgBuffer
}

func (pkg *Package) ToText(w io.Writer, text, prefix, codePrefix string) {
	d := pkg.doc.Parser().Parse(text)
	pr := pkg.doc.Printer()
	pr.TextPrefix = prefix
	pr.TextCodePrefix = codePrefix
	w.Write(pr.Text(d))
}

// pkgBuffer is a wrapper for bytes.Buffer that prints a package clause the
// first time Write is called.
type pkgBuffer struct {
	pkg     *Package
	printed bool // Prevent repeated package clauses.
	bytes.Buffer
}

func (pb *pkgBuffer) Write(p []byte) (int, error) {
	pb.packageClause()
	return pb.Buffer.Write(p)
}

// WriteString must trigger the package clause exactly as Write does;
// without this override the method promoted from the embedded Buffer
// would bypass the clause. (Upstream has no styled writes and so never
// calls WriteString on the buffer.)
func (pb *pkgBuffer) WriteString(s string) (int, error) {
	pb.packageClause()
	return pb.Buffer.WriteString(s)
}

func (pb *pkgBuffer) packageClause() {
	if !pb.printed {
		pb.printed = true
		// Only show package clause for commands if requested explicitly.
		if pb.pkg.pkg.Name != "main" || pb.pkg.opts.ShowCmd {
			pb.pkg.packageClause()
		}
	}
}

// PackageError is the type of errors raised by Fatalf and recovered
// by Render.
type PackageError string

func (p PackageError) Error() string {
	return string(p)
}

// PrettyPath returns a version of the package path that is suitable for an
// error message. It obeys the import comment if present. Also, since
// pkg.build.ImportPath is sometimes the unhelpful "" or ".", it looks for a
// directory name in GOROOT or GOPATH if that happens.
func (pkg *Package) PrettyPath() string {
	path := pkg.build.ImportComment
	if path == "" {
		path = pkg.build.ImportPath
	}
	if path != "." && path != "" {
		return path
	}
	// Convert the source directory into a more useful path.
	// Also convert everything to slash-separated paths for uniform handling.
	path = filepath.Clean(filepath.ToSlash(pkg.build.Dir))
	// Can we find a decent prefix?
	if goroot := build.Default.GOROOT; goroot != "" {
		if p, ok := trim(path, filepath.ToSlash(filepath.Join(goroot, "src"))); ok {
			return p
		}
	}
	for _, gopath := range filepath.SplitList(build.Default.GOPATH) {
		if p, ok := trim(path, filepath.ToSlash(gopath)); ok {
			return p
		}
	}
	return path
}

// trim trims the directory prefix from the path, paying attention
// to the path separator. If they are the same string or the prefix
// is not present the original is returned. The boolean reports whether
// the prefix is present. That path and prefix have slashes for separators.
func trim(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return path, false
	}
	if path == prefix {
		return path, true
	}
	if path[len(prefix)] == '/' {
		return path[len(prefix)+1:], true
	}
	return path, false // Textual prefix but not a path prefix.
}

// Fatalf is like log.Fatalf, but panics so it can be recovered in
// Render, so it doesn't cause an exit. Allows testing to work without
// running a subprocess.
func (pkg *Package) Fatalf(format string, args ...any) {
	panic(PackageError(fmt.Sprintf(format, args...)))
}

// New turns the build package we found into a parsed package
// we can then use to generate documentation. It is a port of
// go doc's parsePackage.
func New(bpkg *build.Package, userPath string, opts Options) (*Package, error) {
	// include tells parser.ParseDir which files to include.
	// That means the file must be in the build package's GoFiles or CgoFiles
	// list only (no tag-ignored files, tests, swig or other non-Go files).
	include := func(info fs.FileInfo) bool {
		for _, name := range bpkg.GoFiles {
			if name == info.Name() {
				return true
			}
		}
		for _, name := range bpkg.CgoFiles {
			if name == info.Name() {
				return true
			}
		}
		return false
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, bpkg.Dir, include, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	// Make sure they are all in one package.
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no source-code package in directory %s", bpkg.Dir)
	}
	if len(pkgs) > 1 {
		return nil, fmt.Errorf("multiple packages in directory %s", bpkg.Dir)
	}
	astPkg := pkgs[bpkg.Name]

	// TODO(upstream): go/doc does not include typed constants in the
	// constants list, which is what we want. For instance, time.Sunday
	// is of type time.Weekday, so it is defined in the type but not in
	// the Consts list for the package. This prevents
	//	go doc time.Sunday
	// from finding the symbol. Work around this for now.
	mode := doc.AllDecls
	if opts.Src {
		mode |= doc.PreserveAST // See comment for Package.emit.
	}
	docPkg := doc.New(astPkg, bpkg.ImportPath, mode)
	typedValue := make(map[*doc.Value]bool)
	constructor := make(map[*doc.Func]bool)
	for _, typ := range docPkg.Types {
		docPkg.Consts = append(docPkg.Consts, typ.Consts...)
		docPkg.Vars = append(docPkg.Vars, typ.Vars...)
		docPkg.Funcs = append(docPkg.Funcs, typ.Funcs...)
		if opts.Unexported || token.IsExported(typ.Name) {
			for _, value := range typ.Consts {
				typedValue[value] = true
			}
			for _, value := range typ.Vars {
				typedValue[value] = true
			}
			for _, fun := range typ.Funcs {
				// We don't count it as a constructor bound to the type
				// if the type itself is not exported.
				constructor[fun] = true
			}
		}
	}

	p := &Package{
		opts:        opts,
		name:        bpkg.Name,
		userPath:    userPath,
		pkg:         astPkg,
		file:        ast.MergePackageFiles(astPkg, 0),
		doc:         docPkg,
		typedValue:  typedValue,
		constructor: constructor,
		build:       bpkg,
		fs:          fset,
	}
	p.buf.pkg = p
	return p, nil
}

// Render formats the documentation selected by symbol and method,
// following the dispatch in go doc's do: an empty symbol renders the
// package, an empty method renders the symbol, and otherwise the
// method or field of symbol is rendered. The found result reports
// whether anything matched; when it is false the output is empty,
// matching go doc, and the caller is expected to report the miss.
func (pkg *Package) Render(symbol, method string) (out string, found bool, err error) {
	defer func() {
		if e := recover(); e != nil {
			pkgError, ok := e.(PackageError)
			if !ok {
				panic(e)
			}
			err = pkgError
		}
	}()
	switch {
	case symbol == "":
		pkg.packageDoc() // The package exists, so we got some output.
		found = true
	case method == "":
		found = pkg.symbolDoc(symbol)
	case pkg.printMethodDoc(symbol, method):
		found = true
	case pkg.printFieldDoc(symbol, method):
		found = true
	}
	return pkg.buf.String(), found, nil
}

func (pkg *Package) Printf(format string, args ...any) {
	fmt.Fprintf(&pkg.buf, format, args...)
}

var newlineBytes = []byte("\n\n") // We never ask for more than 2.

// newlines guarantees there are n newlines at the end of the buffer.
func (pkg *Package) newlines(n int) {
	for !bytes.HasSuffix(pkg.buf.Bytes(), newlineBytes[:n]) {
		pkg.buf.WriteRune('\n')
	}
}

// emit prints the node. If opts.Src is true, it ignores the provided
// comment, assuming the comment is in the node itself. Otherwise, the
// go/doc package clears the stuff we don't want to print anyway. It's
// a bit of a magic trick.
func (pkg *Package) emit(comment string, node ast.Node) {
	if node != nil {
		var arg any = node
		if pkg.opts.Src {
			// Need an extra little dance to get internal comments to appear.
			arg = &printer.CommentedNode{
				Node:     node,
				Comments: pkg.file.Comments,
			}
		}
		var decl bytes.Buffer
		err := format.Node(&decl, pkg.fs, arg)
		if err != nil {
			pkg.Fatalf("%v", err)
		}
		pkg.buf.WriteString(pkg.opts.Style.decl(decl.String()))
		if comment != "" && !pkg.opts.Src {
			pkg.newlines(1)
			pkg.ToText(&pkg.buf, comment, indent, indent+indent)
			pkg.newlines(2) // Blank line after comment to separate from next item.
		} else {
			pkg.newlines(1)
		}
	}
}

// oneLineNode returns a one-line summary of the given input node.
func (pkg *Package) oneLineNode(node ast.Node) string {
	const maxDepth = 10
	return pkg.oneLineNodeDepth(node, maxDepth)
}

// oneLineNodeDepth returns a one-line summary of the given input node.
// The depth specifies the maximum depth when traversing the AST.
func (pkg *Package) oneLineNodeDepth(node ast.Node, depth int) string {
	const dotDotDot = "..."
	if depth == 0 {
		return dotDotDot
	}
	depth--

	switch n := node.(type) {
	case nil:
		return ""

	case *ast.GenDecl:
		// Formats const and var declarations.
		trailer := ""
		if len(n.Specs) > 1 {
			trailer = " " + dotDotDot
		}

		// Find the first relevant spec.
		typ := ""
		for i, spec := range n.Specs {
			valueSpec := spec.(*ast.ValueSpec) // Must succeed; we can't mix types in one GenDecl.

			// The type name may carry over from a previous specification in the
			// case of constants and iota.
			if valueSpec.Type != nil {
				typ = fmt.Sprintf(" %s", pkg.oneLineNodeDepth(valueSpec.Type, depth))
			} else if len(valueSpec.Values) > 0 {
				typ = ""
			}

			if !pkg.isExported(valueSpec.Names[0].Name) {
				continue
			}
			val := ""
			if i < len(valueSpec.Values) && valueSpec.Values[i] != nil {
				val = fmt.Sprintf(" = %s", pkg.oneLineNodeDepth(valueSpec.Values[i], depth))
			}
			return fmt.Sprintf("%s %s%s%s%s", n.Tok, valueSpec.Names[0], typ, val, trailer)
		}
		return ""

	case *ast.FuncDecl:
		// Formats func declarations.
		name := n.Name.Name
		recv := pkg.oneLineNodeDepth(n.Recv, depth)
		if len(recv) > 0 {
			recv = "(" + recv + ") "
		}
		fnc := pkg.oneLineNodeDepth(n.Type, depth)
		fnc = strings.TrimPrefix(fnc, "func")
		return fmt.Sprintf("func %s%s%s", recv, name, fnc)

	case *ast.TypeSpec:
		sep := " "
		if n.Assign.IsValid() {
			sep = " = "
		}
		tparams := pkg.formatTypeParams(n.TypeParams, depth)
		return fmt.Sprintf("type %s%s%s%s", n.Name.Name, tparams, sep, pkg.oneLineNodeDepth(n.Type, depth))

	case *ast.FuncType:
		var params []string
		if n.Params != nil {
			for _, field := range n.Params.List {
				params = append(params, pkg.oneLineField(field, depth))
			}
		}
		needParens := false
		var results []string
		if n.Results != nil {
			needParens = needParens || len(n.Results.List) > 1
			for _, field := range n.Results.List {
				needParens = needParens || len(field.Names) > 0
				results = append(results, pkg.oneLineField(field, depth))
			}
		}

		tparam := pkg.formatTypeParams(n.TypeParams, depth)
		param := joinStrings(params)
		if len(results) == 0 {
			return fmt.Sprintf("func%s(%s)", tparam, param)
		}
		result := joinStrings(results)
		if !needParens {
			return fmt.Sprintf("func%s(%s) %s", tparam, param, result)
		}
		return fmt.Sprintf("func%s(%s) (%s)", tparam, param, result)

	case *ast.StructType:
		if n.Fields == nil || len(n.Fields.List) == 0 {
			return "struct{}"
		}
		return "struct{ ... }"

	case *ast.InterfaceType:
		if n.Methods == nil || len(n.Methods.List) == 0 {
			return "interface{}"
		}
		return "interface{ ... }"

	case *ast.FieldList:
		if n == nil || len(n.List) == 0 {
			return ""
		}
		if len(n.List) == 1 {
			return pkg.oneLineField(n.List[0], depth)
		}
		return dotDotDot

	case *ast.FuncLit:
		return pkg.oneLineNodeDepth(n.Type, depth) + " { ... }"

	case *ast.CompositeLit:
		typ := pkg.oneLineNodeDepth(n.Type, depth)
		if len(n.Elts) == 0 {
			return fmt.Sprintf("%s{}", typ)
		}
		return fmt.Sprintf("%s{ %s }", typ, dotDotDot)

	case *ast.ArrayType:
		length := pkg.oneLineNodeDepth(n.Len, depth)
		element := pkg.oneLineNodeDepth(n.Elt, depth)
		return fmt.Sprintf("[%s]%s", length, element)

	case *ast.MapType:
		key := pkg.oneLineNodeDepth(n.Key, depth)
		value := pkg.oneLineNodeDepth(n.Value, depth)
		return fmt.Sprintf("map[%s]%s", key, value)

	case *ast.CallExpr:
		fnc := pkg.oneLineNodeDepth(n.Fun, depth)
		var args []string
		for _, arg := range n.Args {
			args = append(args, pkg.oneLineNodeDepth(arg, depth))
		}
		return fmt.Sprintf("%s(%s)", fnc, joinStrings(args))

	case *ast.UnaryExpr:
		return fmt.Sprintf("%s%s", n.Op, pkg.oneLineNodeDepth(n.X, depth))

	case *ast.Ident:
		return n.Name

	default:
		// As a fallback, use default formatter for all unknown node types.
		buf := new(strings.Builder)
		format.Node(buf, pkg.fs, node)
		s := buf.String()
		if strings.Contains(s, "\n") {
			return dotDotDot
		}
		return s
	}
}

func (pkg *Package) formatTypeParams(list *ast.FieldList, depth int) string {
	if list.NumFields() == 0 {
		return ""
	}
	var tparams []string
	for _, field := range list.List {
		tparams = append(tparams, pkg.oneLineField(field, depth))
	}
	return "[" + joinStrings(tparams) + "]"
}

// oneLineField returns a one-line summary of the field.
func (pkg *Package) oneLineField(field *ast.Field, depth int) string {
	var names []string
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	if len(names) == 0 {
		return pkg.oneLineNodeDepth(field.Type, depth)
	}
	return joinStrings(names) + " " + pkg.oneLineNodeDepth(field.Type, depth)
}

// joinStrings formats the input as a comma-separated list,
// but truncates the list at some reasonable length if necessary.
func joinStrings(ss []string) string {
	var n int
	for i, s := range ss {
		n += len(s) + len(", ")
		if n > punchedCardWidth {
			ss = append(ss[:i:i], "...")
			break
		}
	}
	return strings.Join(ss, ", ")
}

// printHeader prints a header for the section named s, adding a blank line on each side.
func (pkg *Package) printHeader(s string) {
	pkg.Printf("\n%s\n\n", pkg.opts.Style.header(s))
}

// constsDoc prints all const documentation, if any, including a header.
// The one argument is the valueDoc registry.
func (pkg *Package) constsDoc(printed map[*ast.GenDecl]bool) {
	var header bool
	for _, value := range pkg.doc.Consts {
		// Constants and variables come in groups, and valueDoc prints
		// all the items in the group. We only need to find one exported symbol.
		for _, name := range value.Names {
			if pkg.isExported(name) && !pkg.typedValue[value] {
				if !header {
					pkg.printHeader("CONSTANTS")
					header = true
				}
				pkg.valueDoc(value, printed)
				break
			}
		}
	}
}

// varsDoc prints all var documentation, if any, including a header.
// Printed is the valueDoc registry.
func (pkg *Package) varsDoc(printed map[*ast.GenDecl]bool) {
	var header bool
	for _, value := range pkg.doc.Vars {
		// Constants and variables come in groups, and valueDoc prints
		// all the items in the group. We only need to find one exported symbol.
		for _, name := range value.Names {
			if pkg.isExported(name) && !pkg.typedValue[value] {
				if !header {
					pkg.printHeader("VARIABLES")
					header = true
				}
				pkg.valueDoc(value, printed)
				break
			}
		}
	}
}

// funcsDoc prints all func documentation, if any, including a header.
func (pkg *Package) funcsDoc() {
	var header bool
	for _, fun := range pkg.doc.Funcs {
		if pkg.isExported(fun.Name) && !pkg.constructor[fun] {
			if !header {
				pkg.printHeader("FUNCTIONS")
				header = true
			}
			pkg.emit(fun.Doc, fun.Decl)
		}
	}
}

// typesDoc prints all type documentation, if any, including a header.
func (pkg *Package) typesDoc() {
	var header bool
	for _, typ := range pkg.doc.Types {
		if pkg.isExported(typ.Name) {
			if !header {
				pkg.printHeader("TYPES")
				header = true
			}
			pkg.typeDoc(typ)
		}
	}
}

// packageDoc prints the docs for the package.
func (pkg *Package) packageDoc() {
	pkg.Printf("") // Trigger the package clause; we know the package exists.
	if pkg.opts.All || !pkg.opts.Short {
		pkg.ToText(&pkg.buf, pkg.doc.Doc, "", indent)
		pkg.newlines(1)
	}

	switch {
	case pkg.opts.All:
		printed := make(map[*ast.GenDecl]bool) // valueDoc registry
		pkg.constsDoc(printed)
		pkg.varsDoc(printed)
		pkg.funcsDoc()
		pkg.typesDoc()

	case pkg.pkg.Name == "main" && !pkg.opts.ShowCmd:
		// Show only package docs for commands.
		return

	default:
		if !pkg.opts.Short {
			pkg.newlines(2) // Guarantee blank line before the components.
		}
		pkg.valueSummary(pkg.doc.Consts, false)
		pkg.valueSummary(pkg.doc.Vars, false)
		pkg.funcSummary(pkg.doc.Funcs, false)
		pkg.typeSummary()
	}

	if !pkg.opts.Short {
		pkg.bugs()
	}
}

// packageClause prints the package clause.
func (pkg *Package) packageClause() {
	if pkg.opts.Short {
		return
	}
	importPath := pkg.build.ImportComment
	if importPath == "" {
		importPath = pkg.build.ImportPath
	}

	// The import path derived from module code locations wins: if we
	// started with a directory name, we never knew the import path, and
	// it's cheap to (re)compute it. This mirrors go doc's module-mode
	// scan of its code roots.
	for _, root := range modindex.Roots() {
		if pkg.build.Dir == root.Dir {
			importPath = root.ImportPath
			break
		}
		if strings.HasPrefix(pkg.build.Dir, root.Dir+string(filepath.Separator)) {
			suffix := filepath.ToSlash(pkg.build.Dir[len(root.Dir)+1:])
			if root.ImportPath == "" {
				importPath = suffix
			} else {
				importPath = root.ImportPath + "/" + suffix
			}
			break
		}
	}

	clause := fmt.Sprintf("package %s // import %q", pkg.name, importPath)
	pkg.Printf("%s\n\n", pkg.opts.Style.clause(clause))
}

// valueSummary prints a one-line summary for each set of values and constants.
// If all the types in a constant or variable declaration belong to the same
// type they can be printed by typeSummary, and so can be suppressed here.
func (pkg *Package) valueSummary(values []*doc.Value, showGrouped bool) {
	var isGrouped map[*doc.Value]bool
	if !showGrouped {
		isGrouped = make(map[*doc.Value]bool)
		for _, typ := range pkg.doc.Types {
			if !pkg.isExported(typ.Name) {
				continue
			}
			for _, c := range typ.Consts {
				isGrouped[c] = true
			}
			for _, v := range typ.Vars {
				isGrouped[v] = true
			}
		}
	}

	for _, value := range values {
		if !isGrouped[value] {
			if decl := pkg.oneLineNode(value.Decl); decl != "" {
				pkg.Printf("%s\n", pkg.opts.Style.decl(decl))
			}
		}
	}
}

// funcSummary prints a one-line summary for each function. Constructors
// are printed by typeSummary, below, and so can be suppressed here.
func (pkg *Package) funcSummary(funcs []*doc.Func, showConstructors bool) {
	for _, fun := range funcs {
		// Exported functions only. The go/doc package does not include methods here.
		if pkg.isExported(fun.Name) {
			if showConstructors || !pkg.constructor[fun] {
				pkg.Printf("%s\n", pkg.opts.Style.decl(pkg.oneLineNode(fun.Decl)))
			}
		}
	}
}

// typeSummary prints a one-line summary for each type, followed by its constructors.
func (pkg *Package) typeSummary() {
	for _, typ := range pkg.doc.Types {
		for _, spec := range typ.Decl.Specs {
			typeSpec := spec.(*ast.TypeSpec) // Must succeed.
			if pkg.isExported(typeSpec.Name.Name) {
				pkg.Printf("%s\n", pkg.opts.Style.decl(pkg.oneLineNode(typeSpec)))
				// Now print the consts, vars, and constructors.
				for _, c := range typ.Consts {
					if decl := pkg.oneLineNode(c.Decl); decl != "" {
						pkg.Printf(indent+"%s\n", pkg.opts.Style.decl(decl))
					}
				}
				for _, v := range typ.Vars {
					if decl := pkg.oneLineNode(v.Decl); decl != "" {
						pkg.Printf(indent+"%s\n", pkg.opts.Style.decl(decl))
					}
				}
				for _, constructor := range typ.Funcs {
					if pkg.isExported(constructor.Name) {
						pkg.Printf(indent+"%s\n", pkg.opts.Style.decl(pkg.oneLineNode(constructor.Decl)))
					}
				}
			}
		}
	}
}

// bugs prints the BUGS information for the package.
func (pkg *Package) bugs() {
	if pkg.doc.Notes["BUG"] == nil {
		return
	}
	pkg.Printf("\n")
	for _, note := range pkg.doc.Notes["BUG"] {
		pkg.Printf("%s: %v\n", "BUG", note.Body)
	}
}

// findValues finds the doc.Values that describe the symbol.
func (pkg *Package) findValues(symbol string, docValues []*doc.Value) (values []*doc.Value) {
	for _, value := range docValues {
		for _, name := range value.Names {
			if pkg.match(symbol, name) {
				values = append(values, value)
			}
		}
	}
	return
}

// findFuncs finds the doc.Funcs that describes the symbol.
func (pkg *Package) findFuncs(symbol string) (funcs []*doc.Func) {
	for _, fun := range pkg.doc.Funcs {
		if pkg.match(symbol, fun.Name) {
			funcs = append(funcs, fun)
		}
	}
	return
}

// findTypes finds the doc.Types that describes the symbol.
// If symbol is empty, it finds all exported types.
func (pkg *Package) findTypes(symbol string) (types []*doc.Type) {
	for _, typ := range pkg.doc.Types {
		if symbol == "" && pkg.isExported(typ.Name) || pkg.match(symbol, typ.Name) {
			types = append(types, typ)
		}
	}
	return
}

// findTypeSpec returns the ast.TypeSpec within the declaration that defines the symbol.
// The name must match exactly.
func (pkg *Package) findTypeSpec(decl *ast.GenDecl, symbol string) *ast.TypeSpec {
	for _, spec := range decl.Specs {
		typeSpec := spec.(*ast.TypeSpec) // Must succeed.
		if symbol == typeSpec.Name.Name {
			return typeSpec
		}
	}
	return nil
}

// symbolDoc prints the docs for symbol. There may be multiple matches.
// If symbol matches a type, output includes its methods factories and associated constants.
// If there is no top-level symbol, symbolDoc looks for methods that match.
func (pkg *Package) symbolDoc(symbol string) bool {
	found := false
	// Functions.
	for _, fun := range pkg.findFuncs(symbol) {
		// Symbol is a function.
		decl := fun.Decl
		pkg.emit(fun.Doc, decl)
		found = true
	}
	// Constants and variables behave the same.
	values := pkg.findValues(symbol, pkg.doc.Consts)
	values = append(values, pkg.findValues(symbol, pkg.doc.Vars)...)
	printed := make(map[*ast.GenDecl]bool) // valueDoc registry
	for _, value := range values {
		pkg.valueDoc(value, printed)
		found = true
	}
	// Types.
	for _, typ := range pkg.findTypes(symbol) {
		pkg.typeDoc(typ)
		found = true
	}
	if !found {
		// See if there are methods.
		if !pkg.printMethodDoc("", symbol) {
			return false
		}
	}
	return true
}

// valueDoc prints the docs for a constant or variable. The printed map records
// which values have been printed already to avoid duplication. Otherwise, a
// declaration like:
//
//	const ( c = 1; C = 2 )
//
// … could be printed twice if the -u flag is set, as it matches twice.
func (pkg *Package) valueDoc(value *doc.Value, printed map[*ast.GenDecl]bool) {
	if printed[value.Decl] {
		return
	}
	// Print each spec only if there is at least one exported symbol in it.
	// (See issue 11008.)
	specs := make([]ast.Spec, 0, len(value.Decl.Specs))
	var typ ast.Expr
	for _, spec := range value.Decl.Specs {
		vspec := spec.(*ast.ValueSpec)

		// The type name may carry over from a previous specification in the
		// case of constants and iota.
		if vspec.Type != nil {
			typ = vspec.Type
		}

		for _, ident := range vspec.Names {
			if pkg.opts.Src || pkg.isExported(ident.Name) {
				if vspec.Type == nil && vspec.Values == nil && typ != nil {
					// This a standalone identifier, as in the case of iota usage.
					// Thus, assume the type comes from the previous type.
					vspec.Type = &ast.Ident{
						Name:    pkg.oneLineNode(typ),
						NamePos: vspec.End() - 1,
					}
				}

				specs = append(specs, vspec)
				typ = nil // Only inject type on first exported identifier
				break
			}
		}
	}
	if len(specs) == 0 {
		return
	}
	value.Decl.Specs = specs
	pkg.emit(value.Doc, value.Decl)
	printed[value.Decl] = true
}

// typeDoc prints the docs for a type, including constructors and other items
// related to it.
func (pkg *Package) typeDoc(typ *doc.Type) {
	decl := typ.Decl
	spec := pkg.findTypeSpec(decl, typ.Name)
	pkg.trimUnexportedElems(spec)
	// If there are multiple types defined, reduce to just this one.
	if len(decl.Specs) > 1 {
		decl.Specs = []ast.Spec{spec}
	}
	pkg.emit(typ.Doc, decl)
	pkg.newlines(2)
	// Show associated methods, constants, etc.
	if pkg.opts.All {
		printed := make(map[*ast.GenDecl]bool) // valueDoc registry
		// We can use append here to print consts, then vars. Ditto for funcs and methods.
		values := typ.Consts
		values = append(values, typ.Vars...)
		for _, value := range values {
			for _, name := range value.Names {
				if pkg.isExported(name) {
					pkg.valueDoc(value, printed)
					break
				}
			}
		}
		funcs := typ.Funcs
		funcs = append(funcs, typ.Methods...)
		for _, fun := range funcs {
			if pkg.isExported(fun.Name) {
				pkg.emit(fun.Doc, fun.Decl)
				if fun.Doc == "" {
					pkg.newlines(2)
				}
			}
		}
	} else {
		pkg.valueSummary(typ.Consts, true)
		pkg.valueSummary(typ.Vars, true)
		pkg.funcSummary(typ.Funcs, true)
		pkg.funcSummary(typ.Methods, true)
	}
}

// trimUnexportedElems modifies spec in place to elide unexported fields from
// structs and methods from interfaces (unless the unexported flag is set or we
// are asked to show the original source).
func (pkg *Package) trimUnexportedElems(spec *ast.TypeSpec) {
	if pkg.opts.Src {
		return
	}
	switch typ := spec.Type.(type) {
	case *ast.StructType:
		typ.Fields = pkg.trimUnexportedFields(typ.Fields, false)
	case *ast.InterfaceType:
		typ.Methods = pkg.trimUnexportedFields(typ.Methods, true)
	}
}

// trimUnexportedFields returns the field list trimmed of unexported fields.
func (pkg *Package) trimUnexportedFields(fields *ast.FieldList, isInterface bool) *ast.FieldList {
	what := "methods"
	if !isInterface {
		what = "fields"
	}

	trimmed := false
	list := make([]*ast.Field, 0, len(fields.List))
	for _, field := range fields.List {
		// When printing fields we normally print field.Doc.
		// Here we are going to pass the AST to go/format,
		// which will print the comments from the AST,
		// not field.Doc which is from go/doc.
		// The two are similar but not identical;
		// for example, field.Doc does not include directives.
		// In order to consistently print field.Doc,
		// we replace the comment in the AST with field.Doc.
		// That will cause go/format to print what we want.
		// See issue #56592.
		if field.Doc != nil {
			doc := field.Doc
			text := doc.Text()

			trailingBlankLine := len(doc.List[len(doc.List)-1].Text) == 2
			if !trailingBlankLine {
				// Remove trailing newline.
				lt := len(text)
				if lt > 0 && text[lt-1] == '\n' {
					text = text[:lt-1]
				}
			}

			start := doc.List[0].Slash
			doc.List = doc.List[:0]
			for line := range strings.SplitSeq(text, "\n") {
				prefix := "// "
				if len(line) > 0 && line[0] == '\t' {
					prefix = "//"
				}
				doc.List = append(doc.List, &ast.Comment{
					Text: prefix + line,
				})
			}
			doc.List[0].Slash = start
		}

		names := field.Names
		if len(names) == 0 {
			// Embedded type. Use the name of the type. It must be of the form ident or
			// pkg.ident (for structs and interfaces), or *ident or *pkg.ident (structs only).
			// Or a type embedded in a constraint.
			// Nothing else is allowed.
			ty := field.Type
			if se, ok := field.Type.(*ast.StarExpr); !isInterface && ok {
				// The form *ident or *pkg.ident is only valid on
				// embedded types in structs.
				ty = se.X
			}
			constraint := false
			switch ident := ty.(type) {
			case *ast.Ident:
				if isInterface && ident.Obj == nil &&
					(ident.Name == "error" || ident.Name == "comparable") {
					// For documentation purposes, we consider the builtin error
					// and comparable types special when embedded in an interface,
					// such that they always get shown publicly.
					list = append(list, field)
					continue
				}
				names = []*ast.Ident{ident}
			case *ast.SelectorExpr:
				// An embedded type may refer to a type in another package.
				names = []*ast.Ident{ident.Sel}
			default:
				// An approximation or union or type
				// literal in an interface.
				constraint = true
			}
			if names == nil && !constraint {
				// Can only happen if AST is incorrect. Safe to continue with a nil list.
				continue
			}
		}
		// Trims if any is unexported. Good enough in practice.
		ok := true
		if !pkg.opts.Unexported {
			for _, name := range names {
				if !token.IsExported(name.Name) {
					trimmed = true
					ok = false
					break
				}
			}
		}
		if ok {
			list = append(list, field)
		}
	}
	if !trimmed {
		return fields
	}
	unexportedField := &ast.Field{
		Type: &ast.Ident{
			// Hack: printer will treat this as a field with a named type.
			// Setting Name and NamePos to ("", fields.Closing-1) ensures that
			// when Pos and End are called on this field, they return the
			// position right before closing '}' character.
			Name:    "",
			NamePos: fields.Closing - 1,
		},
		Comment: &ast.CommentGroup{
			List: []*ast.Comment{{Text: fmt.Sprintf("// Has unexported %s.\n", what)}},
		},
	}
	return &ast.FieldList{
		Opening: fields.Opening,
		List:    append(list, unexportedField),
		Closing: fields.Closing,
	}
}

// printMethodDoc prints the docs for matches of symbol.method.
// If symbol is empty, it prints all methods for any concrete type
// that match the name. It reports whether it found any methods.
func (pkg *Package) printMethodDoc(symbol, method string) bool {
	types := pkg.findTypes(symbol)
	if types == nil {
		if symbol == "" {
			return false
		}
		pkg.Fatalf("symbol %s is not a type in package %s installed in %q", symbol, pkg.name, pkg.build.ImportPath)
	}
	found := false
	for _, typ := range types {
		if len(typ.Methods) > 0 {
			for _, meth := range typ.Methods {
				if pkg.match(method, meth.Name) {
					decl := meth.Decl
					pkg.emit(meth.Doc, decl)
					found = true
				}
			}
			continue
		}
		if symbol == "" {
			continue
		}
		// Type may be an interface. The go/doc package does not attach
		// an interface's methods to the doc.Type. We need to dig around.
		spec := pkg.findTypeSpec(typ.Decl, typ.Name)
		inter, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			// Not an interface type.
			continue
		}

		// Collect and print only the methods that match.
		var methods []*ast.Field
		for _, iMethod := range inter.Methods.List {
			// This is an interface, so there can be only one name.
			if len(iMethod.Names) == 0 {
				continue
			}
			name := iMethod.Names[0].Name
			if pkg.match(method, name) {
				methods = append(methods, iMethod)
				found = true
			}
		}
		if found {
			var decl bytes.Buffer
			fmt.Fprintf(&decl, "type %s ", spec.Name)
			inter.Methods.List, methods = methods, inter.Methods.List
			err := format.Node(&decl, pkg.fs, inter)
			if err != nil {
				pkg.Fatalf("%v", err)
			}
			// Restore the original methods.
			inter.Methods.List = methods
			pkg.buf.WriteString(pkg.opts.Style.decl(decl.String()))
			pkg.newlines(1)
		}
	}
	return found
}

// printFieldDoc prints the docs for matches of symbol.fieldName.
// It reports whether it found any field.
// Both symbol and fieldName must be non-empty or it returns false.
func (pkg *Package) printFieldDoc(symbol, fieldName string) bool {
	if symbol == "" || fieldName == "" {
		return false
	}
	types := pkg.findTypes(symbol)
	if types == nil {
		pkg.Fatalf("symbol %s is not a type in package %s installed in %q", symbol, pkg.name, pkg.build.ImportPath)
	}
	found := false
	numUnmatched := 0
	var block bytes.Buffer
	for _, typ := range types {
		// Type must be a struct.
		spec := pkg.findTypeSpec(typ.Decl, typ.Name)
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			// Not a struct type.
			continue
		}
		for _, field := range structType.Fields.List {
			for _, name := range field.Names {
				if !pkg.match(fieldName, name.Name) {
					numUnmatched++
					continue
				}
				if !found {
					fmt.Fprintf(&block, "type %s struct {\n", typ.Name)
				}
				if field.Doc != nil {
					// To present indented blocks in comments correctly, process the comment as
					// a unit before adding the leading // to each line.
					docBuf := new(bytes.Buffer)
					pkg.ToText(docBuf, field.Doc.Text(), "", indent)
					scanner := bufio.NewScanner(docBuf)
					for scanner.Scan() {
						fmt.Fprintf(&block, "%s// %s\n", indent, scanner.Bytes())
					}
				}
				s := pkg.oneLineNode(field.Type)
				lineComment := ""
				if field.Comment != nil {
					lineComment = fmt.Sprintf("  %s", field.Comment.List[0].Text)
				}
				fmt.Fprintf(&block, "%s%s %s%s\n", indent, name, s, lineComment)
				found = true
			}
		}
	}
	if found {
		if numUnmatched > 0 {
			fmt.Fprintf(&block, "\n    // ... other fields elided ...\n")
		}
		fmt.Fprintf(&block, "}\n")
		pkg.buf.WriteString(pkg.opts.Style.decl(block.String()))
	}
	return found
}

// isExported reports whether the name is an exported identifier.
// If the unexported flag (-u) is true, isExported returns true because
// it means that we treat the name as if it is exported.
func (pkg *Package) isExported(name string) bool {
	return pkg.opts.Unexported || token.IsExported(name)
}

// match reports whether the user's symbol matches the program's.
// A lower-case character in the user's string matches either case in the program's.
// The program string must be exported.
func (pkg *Package) match(user, program string) bool {
	if !pkg.isExported(program) {
		return false
	}
	if pkg.opts.CaseSensitive {
		return user == program
	}
	for _, u := range user {
		p, w := utf8.DecodeRuneInString(program)
		program = program[w:]
		if u == p {
			continue
		}
		if unicode.IsLower(u) && simpleFold(u) == simpleFold(p) {
			continue
		}
		return false
	}
	return program == ""
}

// simpleFold returns the minimum rune equivalent to r
// under Unicode-defined simple case folding.
func simpleFold(r rune) rune {
	for {
		r1 := unicode.SimpleFold(r)
		if r1 <= r {
			return r1 // wrapped around, found min
		}
		r = r1
	}
}
