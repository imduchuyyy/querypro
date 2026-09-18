package tui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	sidebarWidth  = 36
	maxInputLines = 8
)

type connection struct {
	name string
	kind string
	uri  string
}

func (c connection) safeURI() string {
	u, err := url.Parse(c.uri)
	if err != nil || u.User == nil {
		return c.uri
	}
	return u.Redacted()
}

type entry struct {
	conn   *connection
	at     time.Time
	query  string
	output string
	failed bool
}

type model struct {
	width   int
	height  int
	theme   int
	sidebar bool
	mouse   bool
	conns   []connection
	active  int
	entries []entry
	follow  bool
	input   textarea.Model
	vp      viewport.Model
	dlg     dialog
}

func Run() error {
	_, err := tea.NewProgram(newModel()).Run()
	return err
}

func newModel() *model {
	m := &model{sidebar: true, mouse: true, active: -1}
	m.input = textarea.New()
	m.input.SetPromptFunc(2, func(p textarea.PromptInfo) string {
		if p.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	m.input.ShowLineNumbers = false
	m.input.Placeholder = `Type a query · "/" for commands`
	m.input.KeyMap.InsertNewline.SetKeys("shift+enter", "ctrl+j")
	m.input.Focus()
	m.vp = viewport.New()
	m.applyTheme()
	return m
}

func (m *model) t() theme { return themes[m.theme] }

func (m *model) applyTheme() {
	t := m.t()
	s := textarea.DefaultDarkStyles()
	for _, st := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		st.CursorLine = lipgloss.NewStyle()
		st.EndOfBuffer = lipgloss.NewStyle()
		st.Text = fg(t.text)
		st.Placeholder = fg(t.muted)
		st.Prompt = fg(t.primary)
	}
	s.Cursor.Color = t.primary
	m.input.SetStyles(s)
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.handle(msg)
	m.layout()
	return m, cmd
}

func (m *model) handle(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return nil
	case dialog:
		m.dlg = msg
		return nil
	case tea.MouseWheelMsg:
		m.vp, _ = m.vp.Update(msg)
		return nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return tea.Quit
		}
		if m.dlg != nil {
			break
		}
		switch msg.String() {
		case "ctrl+p":
			return open(m.palette())
		case "/":
			if m.input.Value() == "" {
				return open(m.palette())
			}
		case "tab":
			m.cycle(1)
			return nil
		case "shift+tab":
			m.cycle(-1)
			return nil
		case "ctrl+b":
			m.sidebar = !m.sidebar
			return nil
		case "pgup":
			m.vp.PageUp()
			return nil
		case "pgdown":
			m.vp.PageDown()
			return nil
		case "enter":
			m.submit()
			return nil
		}
	}
	var cmd tea.Cmd
	if m.dlg != nil {
		m.dlg, cmd = m.dlg.update(msg)
		return cmd
	}
	m.input.SetHeight(maxInputLines)
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *model) cycle(step int) {
	if n := len(m.conns); n > 0 {
		m.active = (m.active + step + n) % n
	}
}

func (m *model) submit() {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return
	}
	m.input.Reset()
	m.follow = true
	if m.active < 0 {
		m.entries = append(m.entries, entry{
			at:     time.Now(),
			query:  q,
			output: `no active connection, press "/" and pick Connect`,
			failed: true,
		})
		return
	}
	c := m.conns[m.active]
	m.entries = append(m.entries, entry{
		conn:   &c,
		at:     time.Now(),
		query:  q,
		output: fmt.Sprintf("no %s plugin installed yet", c.kind),
		failed: true,
	})
}

func (m *model) palette() dialog {
	return newList(m.t(), "Commands", false, func() []item {
		return []item{
			{"Connect", "", func() tea.Cmd {
				return open(newConnect(m.t(), m.addConn))
			}},
			{"Switch connection", "tab", func() tea.Cmd {
				return open(m.switcher())
			}},
			{"Remove connection", "", func() tea.Cmd {
				m.removeActive()
				return nil
			}},
			{"Settings", "", func() tea.Cmd { return open(m.settings()) }},
			{"Toggle sidebar", "ctrl+b", func() tea.Cmd {
				m.sidebar = !m.sidebar
				return nil
			}},
			{"Clear output", "", func() tea.Cmd {
				m.entries = nil
				return nil
			}},
			{"Help", "", func() tea.Cmd { return open(m.help()) }},
			{"Quit", "ctrl+c", func() tea.Cmd { return tea.Quit }},
		}
	})
}

func (m *model) addConn(c connection) {
	m.conns = append(m.conns, c)
	m.active = len(m.conns) - 1
}

func (m *model) removeActive() {
	if m.active < 0 {
		return
	}
	m.conns = append(m.conns[:m.active], m.conns[m.active+1:]...)
	m.active = min(m.active, len(m.conns)-1)
}

func (m *model) switcher() dialog {
	return newList(m.t(), "Switch connection", false, func() []item {
		var items []item
		for i, c := range m.conns {
			items = append(items, item{c.name, c.kind, func() tea.Cmd {
				m.active = i
				return nil
			}})
		}
		return items
	})
}

func (m *model) settings() dialog {
	onOff := map[bool]string{true: "on", false: "off"}
	return newList(m.t(), "Settings", true, func() []item {
		return []item{
			{"Theme", m.t().name, func() tea.Cmd {
				m.theme = (m.theme + 1) % len(themes)
				m.applyTheme()
				return nil
			}},
			{"Sidebar", onOff[m.sidebar], func() tea.Cmd {
				m.sidebar = !m.sidebar
				return nil
			}},
			{"Mouse scrolling", onOff[m.mouse], func() tea.Cmd {
				m.mouse = !m.mouse
				return nil
			}},
		}
	})
}

func (m *model) help() dialog {
	keys := [][2]string{
		{"Run query", "enter"},
		{"New line", "shift+enter / ctrl+j"},
		{"Commands", "/ or ctrl+p"},
		{"Switch connection", "tab / shift+tab"},
		{"Toggle sidebar", "ctrl+b"},
		{"Scroll output", "pgup / pgdown"},
		{"Close dialog", "esc"},
		{"Quit", "ctrl+c"},
	}
	return newList(m.t(), "Help", false, func() []item {
		var items []item
		for _, k := range keys {
			items = append(items, item{k[0], k[1], nil})
		}
		return items
	})
}

func (m *model) mainWidth() int {
	if m.sidebar && m.width >= 80 {
		return m.width - sidebarWidth
	}
	return m.width
}

func (m *model) layout() {
	w := m.mainWidth()
	m.input.SetWidth(max(w-8, 1))
	m.input.SetHeight(min(m.input.LineCount(), maxInputLines))
	used := lipgloss.Height(m.promptView()) + lipgloss.Height(m.footerView())
	m.vp.SetWidth(max(w-4, 1))
	m.vp.SetHeight(max(m.height-used-1, 1))
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
		lipgloss.NewStyle().Padding(1, 2, 0).Render(m.vp.View()),
		m.promptView(),
		m.footerView(),
	)
	main = lipgloss.NewStyle().Width(w).Height(m.height).Render(main)
	body := main
	if w < m.width {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.sidebarView(), main)
	}
	if m.dlg != nil {
		box := m.dlg.view(t, max(min(64, m.width-4), 12))
		x := (m.width - lipgloss.Width(box)) / 2
		y := (m.height - lipgloss.Height(box)) / 4
		body = lipgloss.NewCompositor(
			lipgloss.NewLayer(body),
			lipgloss.NewLayer(box).X(x).Y(y).Z(1),
		).Render()
	}
	v := tea.NewView(body)
	v.AltScreen = true
	v.BackgroundColor = t.bg
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
	lines = append(lines, "", fg(t.muted).Render("one terminal · every backend"), "")
	var hints []string
	for _, k := range [][2]string{
		{"/", "open commands"},
		{"tab", "switch connection"},
		{"ctrl+b", "toggle sidebar"},
	} {
		hints = append(hints, fg(t.accent).Width(8).Render(k[0])+fg(t.muted).Render(k[1]))
	}
	lines = append(lines, lipgloss.JoinVertical(lipgloss.Left, hints...))
	block := lipgloss.JoinVertical(lipgloss.Center, lines...)
	return lipgloss.Place(m.vp.Width(), m.vp.Height(), lipgloss.Center, lipgloss.Center, block)
}

func (m *model) outputView() string {
	if len(m.entries) == 0 {
		return m.welcomeView()
	}
	t := m.t()
	w := m.vp.Width()
	gutter := func(lines []string, first, rest string) string {
		for i := range lines {
			if i == 0 {
				lines[i] = first + lines[i]
			} else {
				lines[i] = rest + lines[i]
			}
		}
		return strings.Join(lines, "\n")
	}
	var blocks []string
	for _, e := range m.entries {
		meta := fg(t.muted).Render(e.at.Format("15:04:05"))
		if e.conn != nil {
			meta = badge(t, e.conn.kind, false) + fg(t.text).Render(e.conn.name) +
				"  " + meta
		}
		qs := strings.Split(e.query, "\n")
		qs[0] = spread(
			lipgloss.NewStyle().MaxWidth(max(w-lipgloss.Width(meta)-4, 1)).Render(fg(t.text).Bold(true).Render(qs[0])),
			meta, w-2)
		for i := 1; i < len(qs); i++ {
			qs[i] = fg(t.text).Render(qs[i])
		}
		icon, style := fg(t.success).Render("✓"), fg(t.text)
		if e.failed {
			icon, style = fg(t.err).Render("✗"), fg(t.err)
		}
		out := strings.Split(style.Width(max(w-5, 1)).Render(e.output), "\n")
		blocks = append(blocks,
			gutter(qs, fg(t.primary).Render("❯ "), "  "),
			gutter(out, fg(t.border).Render("╰─ ")+icon+" ", "     "),
			"",
		)
	}
	return strings.Join(blocks, "\n")
}

func (m *model) connLabel() string {
	t := m.t()
	if m.active < 0 {
		return fg(t.muted).Render("no connection")
	}
	c := m.conns[m.active]
	return badge(t, c.kind, true) + " " + fg(t.text).Bold(true).Render(c.name)
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
	mode := pill("QUERY", t.primary, t.bg)
	if m.dlg != nil {
		mode = pill("COMMAND", t.accent, t.bg)
	}
	right := key("enter", "run") + "  " + key("tab", "switch") + "  " + key("ctrl+p", "commands")
	return lipgloss.NewStyle().Padding(1, 2, 0).
		Render(spread(mode, right, m.mainWidth()-4))
}

func (m *model) sidebarView() string {
	t := m.t()
	box := lipgloss.NewStyle().
		Width(sidebarWidth).
		Height(m.height).
		Padding(1, 2).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(t.border)
	inner := sidebarWidth - box.GetHorizontalFrameSize()
	trunc := lipgloss.NewStyle().MaxWidth(inner)
	lines := []string{
		fg(t.primary).Render("◆ ") + fg(t.text).Bold(true).Render("querypro"),
		fg(t.muted).Render("  one terminal · every backend"),
		"",
		spread(fg(t.muted).Bold(true).Render("CONNECTIONS"),
			fg(t.muted).Render(fmt.Sprint(len(m.conns))), inner),
		"",
	}
	if len(m.conns) == 0 {
		lines = append(lines,
			fg(t.muted).Render("nothing here yet"),
			fg(t.accent).Render("/")+fg(t.muted).Render(" → Connect to add one"))
	}
	for i, c := range m.conns {
		bar, name := "  ", fg(t.muted).Render(c.name)
		if i == m.active {
			bar, name = fg(t.primary).Render("▌ "), fg(t.text).Bold(true).Render(c.name)
		}
		lines = append(lines,
			bar+badge(t, c.kind, i == m.active)+" "+name,
			trunc.Render(fg(t.muted).Render("       "+c.safeURI())),
		)
	}
	return box.Render(strings.Join(lines, "\n"))
}
