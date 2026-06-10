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
// styles.
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
	if theme == "light" {
		cfg = styles.LightStyleConfig
		header = header.Foreground(lipgloss.Color("27"))
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

	return render.Style{
		Clause: func(s string) string { return clause.Render(s) },
		Header: func(s string) string { return header.Render(s) },
		Decl:   decl,
	}
}
