package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

const tablineHeight = 2

type point struct{ line, col int }

type area struct {
	y, x0, x1 int
	fn        func() tea.Cmd
}

type selection struct {
	dragging bool
	shown    bool
	from, to point
}

func (s selection) bounds() (point, point) {
	a, b := s.from, s.to
	if b.line < a.line || (b.line == a.line && b.col < a.col) {
		a, b = b, a
	}
	return a, b
}

func (s selection) cols(line, width int) (int, int, bool) {
	a, b := s.bounds()
	if !s.shown || line < a.line || line > b.line {
		return 0, 0, false
	}
	start, end := 0, width
	if line == a.line {
		start = a.col
	}
	if line == b.line {
		end = b.col + 1
	}
	return start, end, start < end
}

type noticeMsg struct{ id int }

var writeClipboard = clipboard.WriteAll

func (m *model) origin() (int, int) {
	ox := 2
	if m.mainWidth() < m.width {
		ox += sidebarWidth
	}
	return ox, tablineHeight + 1
}

func (m *model) toContent(x, y int) point {
	ox, oy := m.origin()
	col := min(max(x-ox, 0), m.vp.Width()-1)
	row := min(max(y-oy, 0), m.vp.Height()-1)
	return point{m.vp.YOffset() + row, col}
}

func (m *model) inOutput(x, y int) bool {
	ox, oy := m.origin()
	return x >= ox && x < ox+m.vp.Width() && y >= oy && y < oy+m.vp.Height()
}

func (m *model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	switch msg.(type) {
	case tea.MouseClickMsg:
		if m.dlg != nil || mouse.Button != tea.MouseLeft {
			return nil
		}
		m.sel = selection{}
		for _, a := range m.clicks {
			if mouse.Y == a.y && mouse.X >= a.x0 && mouse.X < a.x1 {
				return a.fn()
			}
		}
		if m.inOutput(mouse.X, mouse.Y) {
			p := m.toContent(mouse.X, mouse.Y)
			m.sel = selection{dragging: true, from: p, to: p}
		}
	case tea.MouseMotionMsg:
		if m.sel.dragging {
			m.sel.to = m.toContent(mouse.X, mouse.Y)
			m.sel.shown = true
		}
	case tea.MouseReleaseMsg:
		if !m.sel.dragging {
			return nil
		}
		m.sel.dragging = false
		if text := m.selectedText(); m.sel.shown && text != "" {
			return m.copy(text)
		}
		m.sel = selection{}
	}
	return nil
}

func (m *model) selectedText() string {
	lines := strings.Split(ansi.Strip(m.vp.GetContent()), "\n")
	a, b := m.sel.bounds()
	var out []string
	for l := a.line; l <= b.line && l < len(lines); l++ {
		start, end, ok := m.sel.cols(l, ansi.StringWidth(lines[l]))
		if !ok {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Cut(lines[l], start, end), " "))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (m *model) copy(text string) tea.Cmd {
	_ = writeClipboard(text)
	msg := fmt.Sprintf("✓ copied %d characters", len([]rune(text)))
	return tea.Batch(tea.SetClipboard(text), m.flash(fg(m.t().success).Render(msg)))
}

func (m *model) flash(notice string) tea.Cmd {
	m.notice = notice
	m.noticeID++
	id := m.noticeID
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return noticeMsg{id} })
}

func (m *model) highlight(view string) string {
	if !m.sel.shown {
		return view
	}
	t := m.t()
	style := lipgloss.NewStyle().Background(t.primary).Foreground(t.bg)
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		w := ansi.StringWidth(line)
		start, end, ok := m.sel.cols(m.vp.YOffset()+i, w)
		if !ok {
			continue
		}
		end = min(end, w)
		lines[i] = ansi.Cut(line, 0, start) +
			style.Render(ansi.Strip(ansi.Cut(line, start, end))) +
			ansi.Cut(line, end, w)
	}
	return strings.Join(lines, "\n")
}
