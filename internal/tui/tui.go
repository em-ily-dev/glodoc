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
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"ily.dev/glodoc/internal/modindex"
	"ily.dev/glodoc/internal/render"
	"ily.dev/glodoc/internal/resolve"
	"ily.dev/glodoc/internal/style"
)

// trace is the file logger used when the GLODOC_DEBUG environment
// variable is set. It writes timestamps and millisecond-precision
// stage timings to /tmp/glodoc-debug.log so renders can be analyzed
// after the TUI exits, without disturbing the alt-screen UI.
var trace *log.Logger

func init() {
	if os.Getenv("GLODOC_DEBUG") == "" {
		return
	}
	f, err := os.OpenFile("/tmp/glodoc-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	trace = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	trace.Printf("=== glodoc TUI session started, pid=%d ===", os.Getpid())
}

// tracef writes a formatted line to the debug log if tracing is
// enabled, and is otherwise a no-op.
func tracef(format string, args ...any) {
	if trace != nil {
		trace.Printf(format, args...)
	}
}

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
		style:    style.New(detectStyle()),
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

// detectStyle probes the terminal for its background color and returns
// the style theme to use ("dark" or "light"). It must be called before
// bubble tea takes over input handling, because the OSC response to
// the background-color query is otherwise consumed by bubble tea's
// reader and termenv blocks for a 5-second timeout.
func detectStyle() string {
	t0 := time.Now()
	dark := termenv.NewOutput(os.Stdout).HasDarkBackground()
	tracef("detectStyle dark=%v in %s", dark, ms(time.Since(t0)))
	if dark {
		return "dark"
	}
	return "light"
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

	// style is the colorization resolved once in Run, after detectStyle
	// probed the terminal background but before bubble tea took over
	// the terminal; it is constant for the life of the session.
	style render.Style
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
					tracef("enter pressed for %s", it.path)
					content, err := m.renderPackage(it)
					if err != nil {
						m.err = err
						return m, nil
					}
					setStart := time.Now()
					m.viewport.SetContent(content)
					m.viewport.GotoTop()
					tracef("  viewport.SetContent %s", ms(time.Since(setStart)))
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
// returns it rendered with the session's styling. Loading from the
// directory directly avoids the cost of re-resolving an import path
// through the module graph.
func (m *model) renderPackage(it pkgItem) (string, error) {
	total := time.Now()
	tracef("renderPackage(%s) begin; heap_before=%s", it.path, heapSize())
	defer func() {
		tracef("renderPackage(%s) end total=%s heap_after=%s", it.path, ms(time.Since(total)), heapSize())
	}()

	stage := time.Now()
	t, err := resolve.LoadDir(it.dir)
	tracef("  LoadDir         %s", ms(time.Since(stage)))
	if err != nil {
		return "", err
	}

	stage = time.Now()
	pkg, err := render.New(t.Build, "", render.Options{All: true, Style: m.style})
	if err != nil {
		return "", err
	}
	out, _, err := pkg.Render("", "")
	tracef("  render          %s (%d bytes)", ms(time.Since(stage)), len(out))
	return out, err
}

// ms formats a duration as milliseconds with two decimal places, for
// concise log lines.
func ms(d time.Duration) string {
	return fmt.Sprintf("%6.2fms", float64(d.Microseconds())/1000)
}

// heapSize reports the live heap in a compact form. It exists to spot
// GC events: a sudden drop between two renderPackage calls means a
// collection landed in between.
func heapSize() string {
	var s runtime.MemStats
	runtime.ReadMemStats(&s)
	return fmt.Sprintf("alloc=%dKi numGC=%d", s.HeapAlloc/1024, s.NumGC)
}

var (
	docFrame    = lipgloss.NewStyle().Margin(1, 2)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Reverse(true)
)
