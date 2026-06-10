# glodoc

`go doc`, in color.

glodoc is a drop-in replacement for `go doc`: the same arguments, the
same flags, and the same output text — run it in a terminal and the
text is colorized; pipe it anywhere and the output is byte-for-byte
identical to `go doc`'s. Run it with no arguments and it opens an
interactive browser for the packages of the current module.

## Install

```
go install ily.dev/glodoc@latest
```

## Usage

Everything you already do with `go doc` works unchanged:

```
glodoc fmt                     # package overview
glodoc json                    # shorthand for encoding/json
glodoc fmt.Println             # one symbol
glodoc rand.Intn               # scans crypto/rand, finds math/rand
glodoc json.Number.Int64       # a method
glodoc bytes Buffer.WriteString
glodoc -all errors             # full package documentation
glodoc -src fmt.Errorf         # source code of the declaration
glodoc -u internal/poll        # unexported symbols too
```

The flag set is `go doc`'s: `-all`, `-c`, `-cmd`, `-short`, `-src`,
`-u`, and `-C dir`. (`-http` is not implemented.)

Resolution follows `go doc`'s rules exactly, including the parts that
are easy to get subtly wrong: bare package names resolve through the
standard library, the current module, and the module's dependencies, in
that order; a symbol lookup like `rand.Intn` keeps scanning candidate
packages until one actually contains the symbol; lower-case letters in
a symbol match either case while upper-case letters match exactly.

## What's different

Only presentation. When standard output is a terminal:

- declarations are syntax-highlighted,
- the package clause and section headers are highlighted,
- headings inside doc comments are emphasized, so long package docs
  (think `fmt` or `net/http`) become scannable,
- `Deprecated:` paragraphs and `BUG` notes take a warning tint,
- pre-formatted blocks recede slightly behind the prose,
- output is paginated through `$PAGER` (default `less -FIRX`).

Prose body text is deliberately left plain, and the styling never
changes the text underneath — strip the escape sequences and you have
`go doc`'s output again. When standard output is not a terminal, no
styling is applied at all, so scripts, diffs, and pipelines see exactly
what `go doc` would have printed.

With no arguments, instead of printing the current directory's package
documentation, glodoc opens a TUI listing every package in the current
module with its synopsis: `/` to filter, enter to read a package's
documentation, esc to go back, q to quit.

## The parity guarantee

"Drop-in replacement" is a contract, not an aspiration. glodoc's
resolver and renderer are faithful ports of `go doc`'s own (from
`cmd/go/internal/doc`), kept reviewable side by side with upstream, and
a conformance suite runs both tools against `go doc`'s own test corpus
and compares stdout, stderr, and exit status on every case — plus the
assertions from `go doc`'s own test suite, adapted to the comparison.

Known deviations are tracked as annotations in the suite itself, where
they fail loudly if they ever stop being true. At present there is
exactly one, and it is deliberate: when a symbol lookup on a fully
qualified path fails, `go doc` reports a misleading import error (an
artifact of its own build configuration) where glodoc names the missing
symbol.

If you find any other behavioral difference from `go doc`, it is a bug;
please report it.
