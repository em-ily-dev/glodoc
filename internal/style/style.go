// Package style builds the colorization glodoc applies to rendered
// documentation.
//
// Styling decorates the exact text go doc would print: the renderer
// passes each span unmodified and the decoration adds only escape
// sequences, plus the two padding spaces inside the package clause's
// background highlight that keep it legible — whitespace the
// conformance suite's normalization forgives. Declarations are
// syntax-highlighted through glamour as fenced Go code; the clause and
// section headers take the treatment of glamour's h1 and heading
// styles; within doc comments, headings echo the section-header
// treatment, Deprecated paragraphs and BUG notes take a warning tint,
// and pre-formatted blocks recede behind the prose. Prose body text is
// deliberately left plain.
package style

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"

	"ily.dev/glodoc/internal/render"
)

// New returns the colorization for the given theme, "dark" or "light".
// Theme selection belongs to the caller because probing the terminal
// background must happen while the caller still owns stdin.
func New(theme string) render.Style {
	cfg := styles.DarkStyleConfig
	clause := lipgloss.NewStyle().
		Foreground(lipgloss.Color("228")).
		Background(lipgloss.Color("63")).
		Bold(true).
		Padding(0, 1)
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true)
	warn := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))
	dim := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245"))
	if theme == "light" {
		cfg = styles.LightStyleConfig
		header = header.Foreground(lipgloss.Color("27"))
		warn = warn.Foreground(lipgloss.Color("166"))
		dim = dim.Foreground(lipgloss.Color("240"))
	}

	// Declarations render through glamour as fenced Go code, with the
	// style's margins zeroed so the text keeps go doc's exact layout.
	// Word wrap is disabled: declarations arrive already formatted.
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	cfg.CodeBlock.Margin = &zero
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(0),
	)

	decl := func(s string) string {
		if err != nil {
			return s
		}
		// A four-backtick fence tolerates raw strings containing
		// backquotes inside the declaration.
		out, rerr := renderer.Render("````go\n" + s + "\n````")
		if rerr != nil {
			return s
		}
		return strings.Trim(out, "\n")
	}

	// prose decorates doc-comment text line by line: headings take the
	// header treatment, Deprecated paragraphs a warning tint, and
	// pre-formatted blocks a dimmer tone that sets them off from the
	// surrounding text. Classification is by the indentation the
	// comment printer used, so a wrapped list-item continuation can be
	// mistaken for a pre-formatted block; the cost of that rare miss is
	// a dim line, never altered text.
	prose := func(text, prefix, codePrefix string) string {
		lines := strings.Split(text, "\n")
		deprecated := false
		for i, line := range lines {
			body := strings.TrimPrefix(line, prefix)
			switch {
			case strings.TrimSpace(line) == "":
				deprecated = false
			case strings.HasPrefix(line, codePrefix) && codePrefix != prefix:
				lines[i] = dim.Render(line)
			case strings.HasPrefix(body, "# "):
				lines[i] = header.Render(line)
			case deprecated || strings.HasPrefix(body, "Deprecated:"):
				deprecated = true
				lines[i] = warn.Render(line)
			}
		}
		return strings.Join(lines, "\n")
	}

	return render.Style{
		Clause: func(s string) string { return clause.Render(s) },
		Header: func(s string) string { return header.Render(s) },
		Decl:   decl,
		Prose:  prose,
		Note:   func(s string) string { return warn.Render(s) },
	}
}
