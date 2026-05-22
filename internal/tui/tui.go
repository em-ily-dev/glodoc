// Package tui provides the interactive terminal UI for glodoc.
//
// The interface mirrors glow's: a list of available packages in the
// current module, and a viewport that displays a selected package's
// rendered documentation. Enter opens a package, Esc returns to the
// list, and q (or Ctrl+C) quits.
package tui

import (
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"ily.dev/glodoc/internal/modindex"
	"ily.dev/glodoc/internal/render"
	"ily.dev/glodoc/internal/resolve"
)

// Run starts the TUI, listing the packages of the module rooted at the
// current working directory. It blocks until the user quits.
func Run() error {
	items, err := discoverPackages()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("no Go packages found in the current module")
	}
	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "glodoc"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)

	m := model{
		view:     viewList,
		list:     l,
		viewport: viewport.New(0, 0),
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

// view identifies which surface is currently presented.
type view int

const (
	viewList view = iota
	viewDoc
)

type model struct {
	view     view
	list     list.Model
	viewport viewport.Model
	width    int
	height   int
	err      error
	current  string // package path currently rendered in the viewport
}

// pkgItem is a list entry describing one module package.
type pkgItem struct {
	path     string
	dir      string
	synopsis string
}

func (p pkgItem) Title() string       { return p.path }
func (p pkgItem) Description() string { return p.synopsis }
func (p pkgItem) FilterValue() string { return p.path }

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		hf, vf := docFrame.GetFrameSize()
		m.list.SetSize(msg.Width-hf, msg.Height-vf)
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 1
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.view == viewList && !m.list.SettingFilter() {
				return m, tea.Quit
			}
			if m.view == viewDoc {
				return m, tea.Quit
			}
		case "esc":
			if m.view == viewDoc {
				m.view = viewList
				return m, nil
			}
		case "enter":
			if m.view == viewList {
				if it, ok := m.list.SelectedItem().(pkgItem); ok {
					content, err := renderPackage(it, m.viewport.Width)
					if err != nil {
						m.err = err
						return m, nil
					}
					m.viewport.SetContent(content)
					m.viewport.GotoTop()
					m.current = it.path
					m.view = viewDoc
					return m, nil
				}
			}
		}
	}

	var cmd tea.Cmd
	switch m.view {
	case viewList:
		m.list, cmd = m.list.Update(msg)
	case viewDoc:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if m.err != nil {
		return errorStyle.Render("error: "+m.err.Error()) + "\npress q to quit"
	}
	switch m.view {
	case viewList:
		return docFrame.Render(m.list.View())
	case viewDoc:
		footer := footerStyle.Render(fmt.Sprintf(" %s  ·  esc: list  ·  q: quit ", m.current))
		return m.viewport.View() + "\n" + footer
	}
	return ""
}

// discoverPackages enumerates the packages of the module rooted at the
// current working directory and returns them as list items.
func discoverPackages() ([]list.Item, error) {
	entries := modindex.Default().Module()
	items := make([]list.Item, 0, len(entries))
	for _, e := range entries {
		items = append(items, pkgItem{
			path:     e.ImportPath,
			dir:      e.Dir,
			synopsis: synopsisOf(e.Dir),
		})
	}
	return items, nil
}

// synopsisOf returns the one-line synopsis of the package at dir. It
// parses just enough of the source to read the leading doc comment.
func synopsisOf(dir string) string {
	bpkg, err := build.Default.ImportDir(dir, build.ImportComment)
	if err != nil || len(bpkg.GoFiles) == 0 {
		return ""
	}
	fset := token.NewFileSet()
	for _, name := range bpkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil || f.Doc == nil {
			continue
		}
		text := strings.TrimSpace(f.Doc.Text())
		if i := strings.Index(text, "\n\n"); i >= 0 {
			text = text[:i]
		}
		return strings.Join(strings.Fields(text), " ")
	}
	return ""
}

// renderPackage loads the documentation for the package at it.dir and
// returns it rendered through glamour at the given width. Loading from
// the directory directly avoids the cost of re-resolving an import
// path through the module graph.
func renderPackage(it pkgItem, width int) (string, error) {
	t, err := resolve.LoadDir(it.dir, resolve.Options{})
	if err != nil {
		return "", err
	}
	md := render.Package(t.Pkg, t.Fset, t.Symbol, t.Method, render.Options{All: true})
	wrap := width
	if wrap <= 0 || wrap > 100 {
		wrap = 100
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		return "", err
	}
	defer r.Close()
	return r.Render(md)
}

var (
	docFrame    = lipgloss.NewStyle().Margin(1, 2)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Reverse(true)
)
