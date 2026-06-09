package conformance

// The table below is adapted from the upstream go doc test suite,
// $GOROOT/src/cmd/go/internal/doc/doc_test.go (go1.26.3), preserving
// upstream's cases, order, names, and assertion intent. Every pattern
// was validated against real "go doc" output over this corpus, run
// through the harness's normalize filter.
//
// Because the normalized text is a single line, patterns adapted from
// upstream's line-structured originals are marked ADAPTED with the
// original shown. The conventions:
//
//   - "\n.*" adjacency glue becomes a single literal space, since
//     adjacent tokens collapse to one space.
//   - " +" alignment padding still matches the single space it
//     collapses to and is kept verbatim.
//   - no-patterns that were implicitly line-scoped (upstream "."
//     does not cross newlines) are re-scoped so they cannot
//     false-match across former line boundaries.
//   - exactly one formatting-only sub-assertion (a blank-line gap)
//     is dropped, noted in place.
//
// Upstream quirks — duplicated case names, a never-matching pattern,
// unescaped parens — are preserved verbatim and noted in place, to
// keep this table diffable against upstream.
//
// Three upstream "non-imported" cases (bare-package-name lookup into
// the corpus) are commented out: upstream reaches them only by
// force-feeding testdata directories past go doc's testdata-ignore
// rule with a test-internal hook that has no command-line equivalent.
// They could be revived by copying the corpus into a temporary module
// under a non-"testdata" path; the lookup behavior they cover is real
// parity surface.

var tests = []test{
	// Sanity check.
	{
		name: "sanity check",
		args: []string{p},
		yes:  []string{`type ExportedType struct`},
		diff: "render: legacy markdown chrome",
	},

	// Package dump includes import, package statement.
	{
		name: "package clause",
		args: []string{p},
		// ADAPTED (parameterized): upstream `package pkg.*cmd/go/internal/doc/testdata`
		// hard-codes its testdata import path. pQuoted = regexp.QuoteMeta of the
		// import path printed in our corpus's package clause.
		yes:  []string{`package pkg.*` + pQuoted},
		diff: "render: header shows the argument path, not the canonical import path",
	},

	// Constants.
	// Package dump
	{
		name: "full package",
		args: []string{p},
		// All yes-patterns kept verbatim: go doc's package summary renders each
		// item on one line, so these were already whitespace-run-insensitive
		// (the literal single spaces inside `struct{ ... }` etc. survive the
		// filter unchanged).
		yes: []string{
			`Package comment`,
			`const ExportedConstant = 1`,                                   // Simple constant.
			`const ConstOne = 1`,                                           // First entry in constant block.
			`const ConstFive ...`,                                          // From block starting with unexported constant.
			`var ExportedVariable = 1`,                                     // Simple variable.
			`var VarOne = 1`,                                               // First entry in variable block.
			`func ExportedFunc\(a int\) bool`,                              // Function.
			`func ReturnUnexported\(\) unexportedType`,                     // Function with unexported return type.
			`type ExportedType struct{ ... }`,                              // Exported type.
			`const ExportedTypedConstant ExportedType = iota`,              // Typed constant.
			`const ExportedTypedConstant_unexported unexportedType`,        // Typed constant, exported for unexported type.
			`const ConstLeft2 uint64 ...`,                                  // Typed constant using unexported iota.
			`const ConstGroup1 unexportedType = iota ...`,                  // Typed constant using unexported type.
			`const ConstGroup4 ExportedType = ExportedType{}`,              // Typed constant using exported type.
			`const MultiLineConst = ...`,                                   // Multi line constant.
			`var MultiLineVar = map\[struct{ ... }\]struct{ ... }{ ... }`,  // Multi line variable.
			`func MultiLineFunc\(x interface{ ... }\) \(r struct{ ... }\)`, // Multi line function.
			`var LongLine = newLongLine\(("someArgument[1-4]", ){4}...\)`,  // Long list of arguments.
			`type T1 = T2`,                                                 // Type alias
			`type SimpleConstraint interface{ ... }`,
			`type TildeConstraint interface{ ... }`,
			`type StructConstraint interface{ ... }`,
		},
		// No-patterns kept verbatim; verified none false-match the collapsed
		// package summary.
		no: []string{
			`const internalConstant = 2`,       // No internal constants.
			`var internalVariable = 2`,         // No internal variables.
			`func internalFunc(a int) bool`,    // No internal functions. NOTE: parens unescaped upstream (capture group, matches "func internalFunca int bool"); preserved verbatim for fidelity.
			`Comment about exported constant`,  // No comment for single constant.
			`Comment about exported variable`,  // No comment for single variable.
			`Comment about block of constants`, // No comment for constant block.
			`Comment about block of variables`, // No comment for variable block.
			`Comment before ConstOne`,          // No comment for first entry in constant block.
			`Comment before VarOne`,            // No comment for first entry in variable block.
			`ConstTwo = 2`,                     // No second entry in constant block.
			`VarTwo = 2`,                       // No second entry in variable block.
			`VarFive = 5`,                      // From block starting with unexported variable.
			`type unexportedType`,              // No unexported type.
			`unexportedTypedConstant`,          // No unexported typed constant.
			`\bField`,                          // No fields.
			`Method`,                           // No methods.
			`someArgument[5-8]`,                // No truncated arguments.
			`type T1 T2`,                       // Type alias does not display as type declaration.
			`ignore:directive`,                 // Directives should be dropped.
		},
		diff: "render: one-line declaration summaries not ported (oneLineNode)",
	},
	// Package dump -all
	{
		name: "full package", // upstream reuses this name; kept verbatim
		args: []string{"-all", p},
		yes: []string{
			`package pkg .*import`,
			`Package comment`,
			`CONSTANTS`,
			`Comment before ConstOne`,
			`ConstOne = 1`,
			`ConstTwo = 2 // Comment on line with ConstTwo`,
			`ConstFive`,
			`ConstSix`,
			`Const block where first entry is unexported`,
			`ConstLeft2, constRight2 uint64`,
			`constLeft3, ConstRight3`,
			`ConstLeft4, ConstRight4`,
			`Duplicate = iota`,
			`const CaseMatch = 1`,
			`const Casematch = 2`,
			`const ExportedConstant = 1`,
			`const MultiLineConst = `, // trailing space is real content: "= `MultiLineString1..." follows
			`MultiLineString1`,
			`VARIABLES`,
			`Comment before VarOne`,
			`VarOne = 1`,
			`Comment about block of variables`,
			`VarFive = 5`,
			`var ExportedVariable = 1`,
			`var ExportedVarOfUnExported unexportedType`,
			`var LongLine = newLongLine\(`,
			`var MultiLineVar = map\[struct {`,
			`FUNCTIONS`,
			`func ExportedFunc\(a int\) bool`,
			`Comment about exported function`,
			`func MultiLineFunc\(x interface`,
			`func ReturnUnexported\(\) unexportedType`,
			`TYPES`,
			`type ExportedInterface interface`,
			`type ExportedStructOneField struct`,
			`type ExportedType struct`,
			`Comment about exported type`,
			`const ConstGroup4 ExportedType = ExportedType`,
			`ExportedTypedConstant ExportedType = iota`,
			`Constants tied to ExportedType`,
			`func ExportedTypeConstructor\(\) \*ExportedType`,
			`Comment about constructor for exported type`,
			`func ReturnExported\(\) ExportedType`,
			`func \(ExportedType\) ExportedMethod\(a int\) bool`,
			`Comment about exported method`,
			`type T1 = T2`,
			`type T2 int`,
			`type SimpleConstraint interface {`,
			`type TildeConstraint interface {`,
			`type StructConstraint interface {`,
			`BUG: function body note`,
		},
		no: []string{
			`constThree`,
			`_, _ uint64 = 2 \* iota, 1 << iota`,
			`constLeft1, constRight1`,
			`duplicate`,
			`varFour`,
			`func internalFunc`,
			`unexportedField`,
			`func \(unexportedType\)`,
			`ignore:directive`,
		},
		diff: "render: -all layout (section labels, declaration formatting, notes)",
	},
	// Package with just the package declaration. Issue 31457.
	{
		name: "only package declaration",
		args: []string{"-all", p + "/nested/empty"},
		yes:  []string{`package empty .*import`},
		diff: "render: legacy markdown chrome",
	},
	// Package dump -short
	{
		name: "full package with -short",
		args: []string{`-short`, p},
		yes: []string{
			`const ExportedConstant = 1`,               // Simple constant.
			`func ReturnUnexported\(\) unexportedType`, // Function with unexported return type.
		},
		no: []string{
			`MultiLine(String|Method|Field)`, // No data from multi line portions.
		},
		diff: "render: -short prints full declarations, not one-line summaries",
	},
	// Package dump -u
	{
		name: "full package with u",
		args: []string{`-u`, p},
		yes: []string{
			`const ExportedConstant = 1`,               // Simple constant.
			`const internalConstant = 2`,               // Internal constants.
			`func internalFunc\(a int\) bool`,          // Internal functions.
			`func ReturnUnexported\(\) unexportedType`, // Function with unexported return type.
		},
		no: []string{
			`Comment about exported constant`,  // No comment for simple constant.
			`Comment about block of constants`, // No comment for constant block.
			`Comment about internal function`,  // No comment for internal function.
			`MultiLine(String|Method|Field)`,   // No data from multi line portions.
			`ignore:directive`,
		},
		diff: "render: one-line declaration summaries not ported (oneLineNode)",
	},
	// Package dump -u -all
	{
		name: "full package", // upstream reuses this name; kept verbatim
		args: []string{"-u", "-all", p},
		// ` +=` below is upstream's "one or more spaces then =" (alignment
		// padding); a single space still satisfies ` +`, so kept verbatim.
		yes: []string{
			`package pkg .*import`,
			`Package comment`,
			`CONSTANTS`,
			`Comment before ConstOne`,
			`ConstOne += 1`,
			`ConstTwo += 2 // Comment on line with ConstTwo`,
			`constThree = 3 // Comment on line with constThree`,
			`ConstFive`,
			`const internalConstant += 2`,
			`Comment about internal constant`,
			`VARIABLES`,
			`Comment before VarOne`,
			`VarOne += 1`,
			`Comment about block of variables`,
			`varFour += 4`,
			`VarFive += 5`,
			`varSix += 6`,
			`var ExportedVariable = 1`,
			`var LongLine = newLongLine\(`,
			`var MultiLineVar = map\[struct {`,
			`var internalVariable = 2`,
			`Comment about internal variable`,
			`FUNCTIONS`,
			`func ExportedFunc\(a int\) bool`,
			`Comment about exported function`,
			`func MultiLineFunc\(x interface`,
			`func internalFunc\(a int\) bool`,
			`Comment about internal function`,
			`func newLongLine\(ss .*string\)`,
			`TYPES`,
			`type ExportedType struct`,
			`type T1 = T2`,
			`type T2 int`,
			`type unexportedType int`,
			`Comment about unexported type`,
			`ConstGroup1 unexportedType = iota`,
			`ConstGroup2`,
			`ConstGroup3`,
			`ExportedTypedConstant_unexported unexportedType = iota`,
			`Constants tied to unexportedType`,
			`const unexportedTypedConstant unexportedType = 1`,
			`func ReturnUnexported\(\) unexportedType`,
			`func \(unexportedType\) ExportedMethod\(\) bool`,
			`func \(unexportedType\) unexportedMethod\(\) bool`,
		},
		no: []string{
			`ignore:directive`,
		},
		diff: "render: -all layout (section labels, declaration formatting, notes)",
	},

	// Single constant.
	{
		name: "single constant",
		args: []string{p, `ExportedConstant`},
		yes: []string{
			`Comment about exported constant`, // Include comment.
			`const ExportedConstant = 1`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Single constant -u.
	{
		name: "single constant with -u",
		args: []string{`-u`, p, `internalConstant`},
		yes: []string{
			`Comment about internal constant`, // Include comment.
			`const internalConstant = 2`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Block of constants.
	{
		name: "block of constants",
		args: []string{p, `ConstTwo`},
		yes: []string{
			// ADAPTED: upstream `Comment before ConstOne.\n.*ConstOne = 1`
			// asserted ConstOne = 1 on the line after its doc comment. In
			// filtered text adjacency collapses to one space:
			// "// Comment before ConstOne. ConstOne = 1". Adjacency preserved.
			`Comment before ConstOne\. ConstOne = 1`,      // First...
			`ConstTwo = 2.*Comment on line with ConstTwo`, // And second show up.
			`Comment about block of constants`,            // Comment does too.
		},
		no: []string{
			`constThree`, // No unexported constant.
		},
		diff: "render: legacy markdown chrome",
	},
	// Block of constants -u.
	{
		name: "block of constants with -u",
		args: []string{"-u", p, `constThree`},
		yes: []string{
			`constThree = 3.*Comment on line with constThree`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Block of constants -src.
	{
		name: "block of constants with -src",
		args: []string{"-src", p, `ConstTwo`},
		yes: []string{
			`Comment about block of constants`, // Top comment.
			`ConstOne.*=.*1`,                   // Each constant seen.
			`ConstTwo.*=.*2.*Comment on line with ConstTwo`,
			`constThree`, // Even unexported constants.
		},
		diff: "render: -src prints the go/doc-filtered AST, losing unexported entries",
	},
	// Block of constants with carryover type from unexported field.
	{
		name: "block of constants with carryover type",
		args: []string{p, `ConstLeft2`},
		yes: []string{
			`ConstLeft2, constRight2 uint64`,
			`constLeft3, ConstRight3`,
			`ConstLeft4, ConstRight4`,
		},
		diff: "render: value blocks print the go/doc-filtered AST, with unexported names mangled",
	},
	// Block of constants -u with carryover type from unexported field.
	{
		name: "block of constants with carryover type", // upstream reuses this name; kept verbatim
		args: []string{"-u", p, `ConstLeft2`},
		yes: []string{
			`_, _ uint64 = 2 \* iota, 1 << iota`,
			`constLeft1, constRight1`,
			`ConstLeft2, constRight2`,
			`constLeft3, ConstRight3`,
			`ConstLeft4, ConstRight4`,
		},
		diff: "render: legacy markdown chrome",
	},

	// Single variable.
	{
		name: "single variable",
		args: []string{p, `ExportedVariable`},
		yes: []string{
			`ExportedVariable`, // Include comment.
			`var ExportedVariable = 1`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Single variable -u.
	{
		name: "single variable with -u",
		args: []string{`-u`, p, `internalVariable`},
		yes: []string{
			`Comment about internal variable`, // Include comment.
			`var internalVariable = 2`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Block of variables.
	{
		name: "block of variables",
		args: []string{p, `VarTwo`},
		yes: []string{
			// ADAPTED: upstream `Comment before VarOne.\n.*VarOne = 1`;
			// same line-adjacency rewrite as "block of constants" above.
			`Comment before VarOne\. VarOne = 1`,      // First...
			`VarTwo = 2.*Comment on line with VarTwo`, // And second show up.
			`Comment about block of variables`,        // Comment does too.
		},
		no: []string{
			`varThree= 3`, // No unexported variable. NOTE: missing space is upstream's (pattern can never match); preserved verbatim.
		},
		diff: "render: legacy markdown chrome",
	},
	// Block of variables -u.
	{
		name: "block of variables with -u",
		args: []string{"-u", p, `varThree`},
		yes: []string{
			`varThree = 3.*Comment on line with varThree`,
		},
		diff: "render: legacy markdown chrome",
	},

	// Function.
	{
		name: "function",
		args: []string{p, `ExportedFunc`},
		yes: []string{
			`Comment about exported function`, // Include comment.
			`func ExportedFunc\(a int\) bool`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Function -u.
	{
		name: "function with -u",
		args: []string{"-u", p, `internalFunc`},
		yes: []string{
			`Comment about internal function`, // Include comment.
			`func internalFunc\(a int\) bool`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Function with -src.
	{
		name: "function with -src",
		args: []string{"-src", p, `ExportedFunc`},
		yes: []string{
			`Comment about exported function`, // Include comment.
			`func ExportedFunc\(a int\) bool`,
			`return true != false`, // Include body.
		},
		diff: "render: legacy markdown chrome",
	},

	// Type.
	{
		name: "type",
		args: []string{p, `ExportedType`},
		yes: []string{
			`Comment about exported type`, // Include comment.
			`type ExportedType struct`,    // Type definition.
			// ADAPTED: upstream
			//   `Comment before exported field.*\n.*ExportedField +int` +
			//       `.*Comment on line with exported field`
			// asserted: doc comment line, then on the next line the field name,
			// alignment padding, type, then the line comment. Filtered:
			// "// Comment before exported field. ExportedField int // Comment on
			// line with exported field." Alignment (` +`) collapses to one space
			// (formatting); name/type/comment presence and order kept.
			`Comment before exported field\. ExportedField int.*Comment on line with exported field`,
			`ExportedEmbeddedType.*Comment on line with exported embedded field`,
			`Has unexported fields`,
			`func \(ExportedType\) ExportedMethod\(a int\) bool`,
			`const ExportedTypedConstant ExportedType = iota`, // Must include associated constant.
			`func ExportedTypeConstructor\(\) \*ExportedType`, // Must include constructor.
			`io.Reader.*Comment on line with embedded Reader`,
		},
		no: []string{
			`unexportedField`, // No unexported field.
			// ADAPTED: upstream `int.*embedded` was implicitly line-scoped
			// (upstream `.` does not cross newlines). On collapsed text it would
			// false-match "...ExportedField int // ... field. ... embedded...".
			// Re-scoped with [^.]*: field comments end in a period, so the gap
			// cannot cross into a different field's comment. Still catches an
			// unexported embedded field leaking as its underlying type, e.g.
			// "int // Comment on line with unexported embedded field."
			`int [^.]*embedded`,             // No unexported embedded field.
			`Comment about exported method`, // No comment about exported method.
			`unexportedMethod`,              // No unexported method.
			`unexportedTypedConstant`,       // No unexported constant.
			`error`,                         // No embedded error.
		},
		diff: "render: unexported-elision note differs from go doc wording",
	},
	// Type with -src. Will see unexported fields.
	{
		name: "type", // upstream reuses this name; kept verbatim
		args: []string{"-src", p, `ExportedType`},
		yes: []string{
			`Comment about exported type`, // Include comment.
			`type ExportedType struct`,    // Type definition.
			`Comment before exported field`,
			`ExportedField.*Comment on line with exported field`,
			`ExportedEmbeddedType.*Comment on line with exported embedded field`,
			`unexportedType.*Comment on line with unexported embedded field`,
			`func \(ExportedType\) ExportedMethod\(a int\) bool`,
			`const ExportedTypedConstant ExportedType = iota`, // Must include associated constant.
			`func ExportedTypeConstructor\(\) \*ExportedType`, // Must include constructor.
			`io.Reader.*Comment on line with embedded Reader`,
		},
		no: []string{
			`Comment about exported method`, // No comment about exported method.
			`unexportedMethod`,              // No unexported method.
			`unexportedTypedConstant`,       // No unexported constant.
		},
		diff: "render: -src type omits associated constants, constructors, and methods",
	},
	// Type -all.
	{
		name: "type", // upstream reuses this name; kept verbatim
		args: []string{"-all", p, `ExportedType`},
		yes: []string{
			`type ExportedType struct {`,                        // Type definition as source.
			`Comment about exported type`,                       // Include comment afterwards.
			`const ConstGroup4 ExportedType = ExportedType\{\}`, // Related constants.
			`ExportedTypedConstant ExportedType = iota`,
			`Constants tied to ExportedType`,
			`func ExportedTypeConstructor\(\) \*ExportedType`,
			`Comment about constructor for exported type.`,
			`func ReturnExported\(\) ExportedType`,
			`func \(ExportedType\) ExportedMethod\(a int\) bool`,
			`Comment about exported method.`,
			// ADAPTED: upstream `func \(ExportedType\) Uncommented\(a int\) bool\n\n`
			// ("Ensure line gap after method with no comment"). The trailing
			// `\n\n` is FORMATTING-ONLY (blank-line layout) and is dropped; the
			// content half (the uncommented method appears at all) is kept.
			`func \(ExportedType\) Uncommented\(a int\) bool`,
		},
		no: []string{
			`unexportedType`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Type T1 dump (alias).
	{
		name: "type T1",
		args: []string{p + ".T1"},
		yes: []string{
			`type T1 = T2`,
		},
		no: []string{
			`type T1 T2`,
			`type ExportedType`,
		},
		diff: "resolve: ./dir.Symbol lacks the absolute-path candidate go doc tries",
	},
	// Type -u with unexported fields.
	{
		name: "type with unexported fields and -u",
		args: []string{"-u", p, `ExportedType`},
		yes: []string{
			`Comment about exported type`, // Include comment.
			`type ExportedType struct`,    // Type definition.
			// ADAPTED: upstream `Comment before exported field.*\n.*ExportedField +int`;
			// same adjacency + alignment rewrite as the plain "type" case.
			`Comment before exported field\. ExportedField int`,
			`unexportedField.*int.*Comment on line with unexported field`,
			`ExportedEmbeddedType.*Comment on line with exported embedded field`,
			`\*ExportedEmbeddedType.*Comment on line with exported embedded \*field`,
			`\*qualified.ExportedEmbeddedType.*Comment on line with exported embedded \*selector.field`,
			`unexportedType.*Comment on line with unexported embedded field`,
			`\*unexportedType.*Comment on line with unexported embedded \*field`,
			`io.Reader.*Comment on line with embedded Reader`,
			`error.*Comment on line with embedded error`,
			`func \(ExportedType\) unexportedMethod\(a int\) bool`,
			`unexportedTypedConstant`,
		},
		no: []string{
			`Has unexported fields`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Unexported type with -u.
	{
		name: "unexported type with -u",
		args: []string{"-u", p, `unexportedType`},
		yes: []string{
			`Comment about unexported type`, // Include comment.
			`type unexportedType int`,       // Type definition.
			`func \(unexportedType\) ExportedMethod\(\) bool`,
			`func \(unexportedType\) unexportedMethod\(\) bool`,
			`ExportedTypedConstant_unexported unexportedType = iota`,
			`const unexportedTypedConstant unexportedType = 1`,
		},
		diff: "render: legacy markdown chrome",
	},

	// Interface.
	{
		name: "interface type",
		args: []string{p, `ExportedInterface`},
		yes: []string{
			`Comment about exported interface`, // Include comment.
			`type ExportedInterface interface`, // Interface definition.
			// ADAPTED: upstream's tab-indented multi-line comment-block pattern:
			//   `Comment before exported method.\n.*//\n.*//	// Code block showing how to use ExportedMethod\n.*//	func DoSomething\(\) error {\n.*//		ExportedMethod\(\)\n.*//		return nil\n.*//	}\n.*//.*\n.*ExportedMethod\(\)` +
			//       `.*Comment on line with exported method`
			// The full token sequence of the rendered doc comment (blank comment
			// lines as bare "//", the code block reproduced inside // comments,
			// then the method line with its trailing comment) is content and is
			// preserved verbatim in collapsed form; only the tab indentation of
			// the code block is lost (formatting).
			`Comment before exported method\. // // // Code block showing how to use ExportedMethod // func DoSomething\(\) error { // ExportedMethod\(\) // return nil // } // ExportedMethod\(\).*Comment on line with exported method`,
			`io.Reader.*Comment on line with embedded Reader`,
			`error.*Comment on line with embedded error`,
			`Has unexported methods`,
		},
		no: []string{
			`unexportedField`,               // No unexported field.
			`Comment about exported method`, // No comment about exported method.
			`unexportedMethod`,              // No unexported method.
			`unexportedTypedConstant`,       // No unexported constant.
		},
		diff: "render: unexported-elision note differs from go doc wording",
	},
	// Interface -u with unexported methods.
	{
		name: "interface type with unexported methods and -u",
		args: []string{"-u", p, `ExportedInterface`},
		yes: []string{
			`Comment about exported interface`, // Include comment.
			`type ExportedInterface interface`, // Interface definition.
			// ADAPTED: same comment-block rewrite as "interface type" above.
			`Comment before exported method\. // // // Code block showing how to use ExportedMethod // func DoSomething\(\) error { // ExportedMethod\(\) // return nil // } // ExportedMethod\(\).*Comment on line with exported method`,
			`unexportedMethod\(\).*Comment on line with unexported method`,
			`io.Reader.*Comment on line with embedded Reader`,
			`error.*Comment on line with embedded error`,
		},
		no: []string{
			`Has unexported methods`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Interface with comparable constraint.
	{
		name: "interface type with comparable",
		args: []string{p, `ExportedComparableInterface`},
		yes: []string{
			`Comment about exported interface with comparable`, // Include comment.
			`type ExportedComparableInterface interface`,       // Interface definition.
			`comparable.*Comment on line with comparable`,      // Comparable should be shown.
			`ExportedMethod\(\).*Comment on line with exported method`,
			`Has unexported methods`,
		},
		no: []string{
			`unexportedMethod`, // No unexported method.
		},
		diff: "render: unexported-elision note differs from go doc wording",
	},
	// Interface with only comparable (no unexported methods).
	{
		name: "interface type with comparable only",
		args: []string{p, `ExportedComparableOnlyInterface`},
		yes: []string{
			`ExportedComparableOnlyInterface has only comparable`, // Include comment.
			`type ExportedComparableOnlyInterface interface`,      // Interface definition.
			`comparable.*Comment on line with comparable`,         // Comparable should be shown.
			`ExportedMethod\(\).*Comment on line with exported method`,
		},
		no: []string{
			`Has unexported methods`, // Should NOT appear - no unexported methods.
		},
		diff: "render: legacy markdown chrome",
	},

	// Interface method.
	{
		name: "interface method",
		args: []string{p, `ExportedInterface.ExportedMethod`},
		yes: []string{
			// ADAPTED: same comment-block rewrite as "interface type" above.
			`Comment before exported method\. // // // Code block showing how to use ExportedMethod // func DoSomething\(\) error { // ExportedMethod\(\) // return nil // } // ExportedMethod\(\).*Comment on line with exported method`,
		},
		no: []string{
			`Comment about exported interface`,
		},
		diff: "render: interface method view renders prose, not the source comment block",
	},
	// Interface method at package level.
	{
		name: "interface method at package level",
		args: []string{p, `ExportedMethod`},
		yes: []string{
			`func \(ExportedType\) ExportedMethod\(a int\) bool`,
			`Comment about exported method`,
		},
		no: []string{
			// ADAPTED: upstream
			//   `Comment before exported method.*\n.*ExportedMethod\(\)` +
			//       `.*Comment on line with exported method`
			// asserts the INTERFACE method's doc must not appear when the
			// package-level lookup resolves to the type method. Line-structure
			// glue rewritten; the distinguishing content (interface doc comment
			// followed by the method-with-line-comment rendering) kept.
			`Comment before exported method\..*ExportedMethod\(\) // Comment on line with exported method`,
		},
		diff: "render: package-level method lookup not implemented",
	},

	// Method.
	{
		name: "method",
		args: []string{p, `ExportedType.ExportedMethod`},
		yes: []string{
			`func \(ExportedType\) ExportedMethod\(a int\) bool`,
			`Comment about exported method`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Method  with -u.
	{
		name: "method with -u",
		args: []string{"-u", p, `ExportedType.unexportedMethod`},
		yes: []string{
			`func \(ExportedType\) unexportedMethod\(a int\) bool`,
			`Comment about unexported method`,
		},
		diff: "render: legacy markdown chrome",
	},
	// Method with -src.
	{
		name: "method with -src",
		args: []string{"-src", p, `ExportedType.ExportedMethod`},
		yes: []string{
			`func \(ExportedType\) ExportedMethod\(a int\) bool`,
			`Comment about exported method`,
			`return true != true`,
		},
		diff: "render: legacy markdown chrome",
	},

	// Field.
	{
		name: "field",
		args: []string{p, `ExportedType.ExportedField`},
		yes: []string{
			`type ExportedType struct`,
			`ExportedField int`,
			`Comment before exported field`,
			`Comment on line with exported field`,
			`other fields elided`,
		},
		diff: "render: field view prints nothing (go/printer cannot print a bare ast.Field)",
	},

	// Field with -u.
	{
		name: "method with -u", // upstream's name (sic, it is a field case); kept verbatim
		args: []string{"-u", p, `ExportedType.unexportedField`},
		yes: []string{
			`unexportedField int`,
			`Comment on line with unexported field`,
		},
		diff: "render: field view prints nothing (go/printer cannot print a bare ast.Field)",
	},

	// Field of struct with only one field.
	{
		name: "single-field struct",
		args: []string{p, `ExportedStructOneField.OnlyField`},
		yes:  []string{`the only field`},
		no:   []string{`other fields elided`},
		diff: "render: field view prints nothing (go/printer cannot print a bare ast.Field)",
	},

	// Case matching off.
	{
		name: "case matching off",
		args: []string{p, `casematch`},
		yes: []string{
			`CaseMatch`,
			`Casematch`,
		},
		diff: "render: only the first matching symbol is printed",
	},

	// Case matching on.
	{
		name: "case matching on",
		args: []string{"-c", p, `Casematch`},
		yes: []string{
			`Casematch`,
		},
		no: []string{
			`CaseMatch`,
		},
		diff: "render: legacy markdown chrome",
	},

	// Merging comments with -src.
	{
		name: "merge comments with -src A",
		args: []string{"-src", p + "/merge", `A`},
		yes: []string{
			`A doc`,
			`func A`,
			`A comment`,
		},
		no: []string{
			`Package A doc`,
			`Package B doc`,
			`B doc`,
			`B comment`,
			`B doc`, // upstream lists `B doc` twice; duplicate preserved verbatim
		},
		diff: "render: -src loses comments inside declarations (no printer.CommentedNode)",
	},
	{
		name: "merge comments with -src B",
		args: []string{"-src", p + "/merge", `B`},
		yes: []string{
			`B doc`,
			`func B`,
			`B comment`,
		},
		no: []string{
			`Package A doc`,
			`Package B doc`,
			`A doc`,
			`A comment`,
			`A doc`, // upstream lists `A doc` twice; duplicate preserved verbatim
		},
		diff: "render: -src loses comments inside declarations (no printer.CommentedNode)",
	},

	// No dups with -u. Issue 21797.
	{
		name: "case matching on, no dups",
		args: []string{"-u", p, `duplicate`},
		yes: []string{
			`Duplicate`,
			`duplicate`,
		},
		no: []string{
			// ADAPTED: upstream "\\)\n+const" — a second const declaration
			// directly after the block's closing paren (the issue-21797
			// duplicate). Newline run collapses to a single space.
			// CAVEAT: would also fire on an import path ending in ")", which
			// cannot occur.
			`\) const`, // This will appear if the const decl appears twice.
		},
		diff: "render: legacy markdown chrome",
	},

	// ------------------------------------------------------------------
	// NOT PORTABLE AS-IS: the next three upstream cases ("non-imported")
	// exercise bare-package-name lookup ("nested", "nested.Foo") against
	// testdata/nested/nested, which is reachable upstream ONLY because
	// TestMain calls dirsInit(...) to force-feed the testdata directories
	// into the package scanner — go doc normally ignores any directory
	// named "testdata", and there is no CLI equivalent of that hook.
	//
	// They COULD be revived in an exec harness if the runner copies the
	// corpus into a temp module under a non-"testdata" path (e.g.
	// <tmpmod>/pkg/nested/nested) and runs both binaries with the module as
	// cwd, so that bare-name lookup ("nested") resolves by scanning the
	// current module. Until the harness does that, they are commented out
	// rather than silently dropped — the lookup behavior they cover (bare
	// pkg.sym, bare pkg, bare two-arg) is real parity surface for glodoc's
	// package resolution.
	//
	// {
	// 	"non-imported: pkg.sym",
	// 	[]string{"nested.Foo"},
	// 	[]string{"Foo struct"},
	// 	nil,
	// },
	// {
	// 	"non-imported: pkg only",
	// 	[]string{"nested"},
	// 	[]string{"Foo struct"},
	// 	nil,
	// },
	// {
	// 	"non-imported: pkg sym",
	// 	[]string{"nested", "Foo"},
	// 	[]string{"Foo struct"},
	// 	nil,
	// },
	// ------------------------------------------------------------------

	{
		name: "formatted doc on function",
		args: []string{p, "ExportedFormattedDoc"},
		yes: []string{
			`func ExportedFormattedDoc\(a int\) bool`,
			// ADAPTED: upstream asserted the doc body with go/doc rendering
			// indentation (4-space text, 8-space pre-formatted block):
			//   `    Comment about exported function with formatting\.
			//
			//	    Example
			//
			//	        fmt\.Println\(FormattedDoc\(\)\)
			//
			//	    Text after pre-formatted block\.`
			// Indentation depth is formatting and is lost in the filter; the
			// content — every line of the comment, including the code example,
			// in order — is preserved.
			`Comment about exported function with formatting\. Example fmt\.Println\(FormattedDoc\(\)\) Text after pre-formatted block\.`,
		},
		diff: "render: legacy markdown chrome",
	},
	{
		name: "formatted doc on type field",
		args: []string{p, "ExportedFormattedType.ExportedField"},
		yes: []string{
			`type ExportedFormattedType struct`,
			// ADAPTED: upstream asserted the field doc rendered as //-comments
			// with 4-space struct indentation and trailing-space blank comment
			// lines (`//[ ]`):
			//   `    // Comment before exported field with formatting\.
			//	    //[ ]
			//	    // Example
			//	    //[ ]
			//	    //     a\.ExportedField = 123
			//	    //[ ]
			//	    // Text after pre-formatted block\.`
			// Blank comment lines survive as bare "//" tokens; the trailing
			// space and indentation are formatting and are lost.
			// NOTE: upstream distinguished space-indent here vs tab-indent in
			// the next two cases; that distinction is formatting-only and
			// vanishes — all three collapse to the same pattern.
			`// Comment before exported field with formatting\. // // Example // // a\.ExportedField = 123 // // Text after pre-formatted block\.`,
			`ExportedField int`,
		},
		no:   []string{"ignore:directive"},
		diff: "render: field view prints nothing (go/printer cannot print a bare ast.Field)",
	},
	{
		name: "formatted doc on entire type",
		args: []string{p, "ExportedFormattedType"},
		yes: []string{
			`type ExportedFormattedType struct`,
			// ADAPTED: upstream's tab-indented variant of the previous pattern
			// (`\t// Comment before exported field with formatting\.` ...);
			// same collapse, see note above.
			`// Comment before exported field with formatting\. // // Example // // a\.ExportedField = 123 // // Text after pre-formatted block\.`,
			`ExportedField int`,
		},
		no:   []string{"ignore:directive"},
		diff: "render: comment directives leak into rendered output",
	},
	{
		name: "formatted doc on entire type with -all",
		args: []string{"-all", p, "ExportedFormattedType"},
		yes: []string{
			`type ExportedFormattedType struct`,
			// ADAPTED: identical to the previous case upstream (tab-indented);
			// same collapse, see note above.
			`// Comment before exported field with formatting\. // // Example // // a\.ExportedField = 123 // // Text after pre-formatted block\.`,
			`ExportedField int`,
		},
		no:   []string{"ignore:directive"},
		diff: "render: comment directives leak into rendered output",
	},
}
