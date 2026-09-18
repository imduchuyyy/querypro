package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

func (m *model) mainWidth() int {
	if m.sidebar && m.width >= 80 {
		return m.width - sidebarWidth
	}
	return m.width
}

func (m *model) layout() {
	w := m.mainWidth()
	m.input.Placeholder = `"/" for commands`
	if c := m.conn(); c != nil {
		m.input.Placeholder = kindOf(c.kind).Placeholder + `   · "/" for commands`
	}
	m.input.SetWidth(max(w-8, 1))
	m.input.SetHeight(min(m.input.LineCount(), maxInputLines))
	used := lipgloss.Height(m.promptView()) + lipgloss.Height(m.footerView())
	m.vp.SetWidth(max(w-4, 1))
	m.vp.SetHeight(max(m.height-tablineHeight-used-1, 1))
	m.vp.SetContent(m.outputView())
	if m.follow {
		m.vp.GotoBottom()
		m.follow = false
	}
}

func (m *model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("")
	}
	t := m.t()
	w := m.mainWidth()
	main := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Padding(1, 2, 0).Render(m.highlight(m.vp.View())),
		m.promptView(),
		m.footerView(),
	)
	h := m.height - tablineHeight
	main = lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(main)
	m.clicks = nil
	body := main
	if w < m.width {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.sidebarView(), main)
	}
	body = m.tablineView() + "\n" + body
	if m.dlg != nil {
		box := m.dlg.view(t, max(min(72, m.width-4), 12))
		x := (m.width - lipgloss.Width(box)) / 2
		y := (m.height - lipgloss.Height(box)) / 4
		body = lipgloss.NewCompositor(
			lipgloss.NewLayer(body),
			lipgloss.NewLayer(box).X(x).Y(y).Z(1),
		).Render()
	}
	v := tea.NewView(body)
	v.AltScreen = true
	v.WindowTitle = "querypro"
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

var logo = [2][2]string{
	{"█▀█ █ █ █▀▀ █▀█ █▄█", "█▀█ █▀█ █▀█"},
	{"▀▀█ █▄█ ██▄ █▀▄  █ ", "█▀▀ █▀▄ █▄█"},
}

func (m *model) welcomeView() string {
	t := m.t()
	var lines []string
	for _, row := range logo {
		lines = append(lines, fg(t.text).Render(row[0])+" "+fg(t.primary).Render(row[1]))
	}
	tagline := fg(t.muted).Render("one terminal · every backend")
	keys := [][2]string{
		{"ctrl+t", "open a connection in a new tab"},
		{"/", "commands"},
	}
	if c := m.conn(); c != nil {
		tagline = fg(t.muted).Render("tab ") + fg(t.primary).Render(fmt.Sprint(m.active+1)) +
			fg(t.muted).Render(" · ") + badge(t, c.kind, true) + " " + fg(t.text).Bold(true).Render(c.name) +
			fg(t.muted).Render(" · try ") + fg(t.accent).Render(kindOf(c.kind).Placeholder)
		keys = [][2]string{
			{"ctrl+o", "open a resource"},
			{"ctrl+x", "actions on it"},
			{"tab", "next tab · alt+1..9 jump"},
			{"!pg", "query another tab's connection"},
			{"ctrl+t", "new tab"},
		}
	}
	lines = append(lines, "", tagline, "")
	var hints []string
	for _, k := range keys {
		hints = append(hints, fg(t.accent).Width(8).Render(k[0])+fg(t.muted).Render(k[1]))
	}
	lines = append(lines, lipgloss.JoinVertical(lipgloss.Left, hints...))
	block := lipgloss.JoinVertical(lipgloss.Center, lines...)
	return lipgloss.Place(m.vp.Width(), m.vp.Height(), lipgloss.Center, lipgloss.Center, block)
}

func indent(s, first, rest string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = first + lines[i]
		} else {
			lines[i] = rest + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func (m *model) outputView() string {
	c := m.conn()
	if c == nil || len(c.entries) == 0 {
		return m.welcomeView()
	}
	var blocks []string
	for _, e := range c.entries {
		key := fmt.Sprint(m.vp.Width(), m.theme, e.running, e.live, e.err, len(e.lines), e.elapsed)
		if e.viewKey != key {
			e.view, e.viewKey = m.entryView(e), key
		}
		blocks = append(blocks, e.view, "")
	}
	return strings.Join(blocks, "\n")
}

func (m *model) entryView(e *entry) string {
	t := m.t()
	w := m.vp.Width()
	meta := fg(t.muted).Render(e.at.Format("15:04:05"))
	if e.conn != nil {
		meta = badge(t, e.conn.kind, false) + fg(t.text).Render(e.conn.name) + "  " + meta
	}
	qs := strings.Split(e.query, "\n")
	first := lipgloss.NewStyle().MaxWidth(max(w-lipgloss.Width(meta)-4, 1)).
		Render(fg(t.text).Bold(true).Render(qs[0]))
	qs[0] = spread(first, meta, w-2)
	for i := 1; i < len(qs); i++ {
		qs[i] = fg(t.text).Render(qs[i])
	}
	parts := []string{indent(strings.Join(qs, "\n"), fg(t.primary).Render("❯ "), "  ")}

	took := fmt.Sprintf(" · %dms", e.elapsed.Milliseconds())
	var status string
	switch {
	case e.running:
		status = fg(t.accent).Render("⋯ running") + fg(t.muted).Render(" · esc to cancel")
	case e.err != nil:
		status = fg(t.err).Width(max(w-5, 1)).Render("✗ " + e.err.Error())
	case e.live:
		status = fg(t.success).Render("● "+e.res.Summary) +
			fg(t.muted).Render(fmt.Sprintf(" · live · %d messages · esc to stop", len(e.lines)))
	case e.res.Stream != nil:
		status = fg(t.muted).Render(fmt.Sprintf("■ %s · stopped · %d messages", e.res.Summary, len(e.lines)))
	default:
		status = fg(t.success).Render("✓ ") + fg(t.muted).Render(e.res.Summary+took)
	}
	parts = append(parts, indent(status, fg(t.border).Render("╰─ "), "   "))

	var body []string
	if len(e.res.Columns) > 0 {
		body = append(body, m.tableView(e.res.Columns, e.res.Rows, w-3))
	}
	if e.res.Text != "" {
		body = append(body, fg(t.text).Width(max(w-3, 1)).Render(e.res.Text))
	}
	for _, l := range e.lines {
		c := t.text
		switch {
		case strings.Contains(l, " ERROR "):
			c = t.err
		case strings.Contains(l, " WARN "):
			c = t.accent
		}
		body = append(body, fg(c).Width(max(w-3, 1)).Render(l))
	}
	for _, b := range body {
		parts = append(parts, indent(b, "   ", "   "))
	}
	return strings.Join(parts, "\n")
}

func (m *model) tableView(cols []string, rows [][]string, w int) string {
	t := m.t()
	tb := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(fg(t.border)).
		Headers(cols...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1).Foreground(t.text)
			if row == table.HeaderRow {
				return s.Foreground(t.accent).Bold(true)
			}
			if row < len(rows) && col < len(rows[row]) {
				switch rows[row][col] {
				case "ERROR", "error":
					return s.Foreground(t.err)
				case "WARN", "warn":
					return s.Foreground(t.accent)
				case "NULL":
					return s.Foreground(t.muted)
				}
			}
			return s
		})
	out := tb.Render()
	if lipgloss.Width(out) > w {
		out = tb.Width(max(w, 4)).Render()
	}
	return out
}

func (m *model) connLabel() string {
	t := m.t()
	c := m.conn()
	if c == nil {
		return fg(t.muted).Render("no connection")
	}
	label := badge(t, c.kind, true) + " " + fg(t.text).Bold(true).Render(c.name)
	if c.current != nil {
		label += fg(t.muted).Render(" › ") + fg(t.accent).Render(c.current.Name)
	}
	return label
}

func (m *model) promptView() string {
	t := m.t()
	border := t.primary
	if m.dlg != nil {
		border = t.border
	}
	box := titled(border, m.mainWidth()-4, m.connLabel(), m.input.View(), 1)
	return lipgloss.NewStyle().Margin(1, 2, 0).Render(box)
}

func (m *model) footerView() string {
	t := m.t()
	key := func(k, desc string) string {
		return fg(t.accent).Render(k) + " " + fg(t.muted).Render(desc)
	}
	running, live := busy(m.conn())
	mode := pill("QUERY", t.primary, t.bg)
	switch {
	case m.dlg != nil:
		mode = pill("COMMAND", t.accent, t.bg)
	case live:
		mode = pill("LIVE", t.success, t.bg)
	case running:
		mode = pill("RUNNING", t.accent, t.bg)
	}
	if m.notice != "" {
		mode += " " + m.notice
	}
	hints := []string{key("ctrl+o", "open"), key("ctrl+x", "actions"), key("ctrl+p", "commands")}
	if running || live {
		hints = append([]string{key("esc", "stop")}, hints...)
	}
	w := m.mainWidth() - 4
	return lipgloss.NewStyle().Padding(1, 2, 0).MaxWidth(w + 4).
		Render(spread(mode, strings.Join(hints, "  "), w))
}

func (m *model) sidebarView() string {
	t := m.t()
	h := m.height - tablineHeight
	box := lipgloss.NewStyle().
		Width(sidebarWidth).
		Height(h).
		MaxHeight(h).
		Padding(1, 2).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(t.border)
	inner := sidebarWidth - box.GetHorizontalFrameSize()
	trunc := lipgloss.NewStyle().MaxWidth(inner)
	section := func(title, right string) string {
		return spread(fg(t.muted).Bold(true).Render(title), fg(t.muted).Render(right), inner)
	}
	c := m.conn()
	if c == nil {
		return box.Render(strings.Join([]string{
			section("NO CONNECTION", ""),
			"",
			fg(t.muted).Render("open one in a new tab"),
			fg(t.accent).Render("ctrl+t") + fg(t.muted).Render(" or ") + fg(t.accent).Render("/") +
				fg(t.muted).Render(" → New tab"),
		}, "\n"))
	}
	var state string
	switch {
	case c.connecting:
		state = fg(t.accent).Render("◌ connecting")
	case c.sess != nil:
		state = trunc.Render(fg(t.success).Render("● ") + fg(t.muted).Render(c.sess.Server()))
	default:
		state = fg(t.err).Render("○ disconnected")
	}
	lines := []string{
		section("CONNECTION", ""),
		"",
		badge(t, c.kind, true) + " " + fg(t.text).Bold(true).Render(c.name),
		trunc.Render(fg(t.muted).Render(c.safeURI())),
		state,
	}
	if c.err != nil {
		lines = append(lines, strings.Split(fg(t.err).Width(inner).Render("✗ "+c.err.Error()), "\n")...)
	}
	lines = append(lines, "", section("RESOURCES", fmt.Sprint(len(c.resources))), "")
	for _, r := range c.resources {
		kind := fg(t.muted).Render(r.Kind)
		label := ansi.Truncate(r.Name, max(inner-lipgloss.Width(kind)-3, 1), "…")
		mark, name := "  ", fg(t.text).Render(label)
		if c.current != nil && *c.current == r {
			mark, name = fg(t.accent).Render("▸ "), fg(t.accent).Bold(true).Render(label)
		}
		m.clicks = append(m.clicks, area{
			y: tablineHeight + 1 + len(lines), x0: 0, x1: sidebarWidth,
			fn: func() tea.Cmd { return m.openResource(c, r) },
		})
		lines = append(lines, trunc.Render(spread(mark+name, kind, inner)))
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m *model) tablineView() string {
	t := m.t()
	x := 0
	var parts []string
	add := func(s string, fn func() tea.Cmd) {
		w := lipgloss.Width(s)
		if fn != nil {
			m.clicks = append(m.clicks, area{y: 0, x0: x, x1: x + w, fn: fn})
		}
		parts = append(parts, s)
		x += w
	}
	sep := fg(t.border).Render("│")
	add(fg(t.primary).Render(" ◆ ")+fg(t.text).Bold(true).Render("querypro")+" ", nil)
	add(sep, nil)
	for i, c := range m.conns {
		num, name := fg(t.muted).Render(fmt.Sprint(i+1)), fg(t.muted).Render(c.name)
		if i == m.active {
			num = fg(t.primary).Bold(true).Render(fmt.Sprint(i + 1))
			name = fg(t.text).Bold(true).Underline(true).Render(c.name)
		}
		mark := ""
		switch _, live := busy(c); {
		case live:
			mark = fg(t.success).Render(" ●")
		case c.connecting:
			mark = fg(t.accent).Render(" ◌")
		case c.sess == nil:
			mark = fg(t.err).Render(" ○")
		}
		add(" "+num+" "+badge(t, c.kind, i == m.active)+" "+name+mark+" ", func() tea.Cmd {
			m.switchTab(i)
			return nil
		})
		add(sep, nil)
	}
	add(fg(t.accent).Render(" + new "), func() tea.Cmd {
		return open(m.newTab())
	})
	line := lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(parts, ""))
	return line + "\n" + fg(t.border).Render(strings.Repeat("─", m.width))
}
