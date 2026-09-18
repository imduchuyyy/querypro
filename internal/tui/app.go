package tui

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"querypro/internal/plugin"
)

const (
	sidebarWidth  = 36
	maxInputLines = 8
	maxLiveLines  = 200
)

type connection struct {
	id      int
	name    string
	kind    string
	uri     string
	ready   bool
	plug    plugin.Plugin
	current *plugin.Resource
	entries []*entry
	draft   string
	offset  int
}

func (c *connection) safeURI() string {
	u, err := url.Parse(c.uri)
	if err != nil || u.User == nil {
		return c.uri
	}
	return u.Redacted()
}

type entry struct {
	id      int
	conn    *connection
	at      time.Time
	query   string
	running bool
	live    bool
	elapsed time.Duration
	res     plugin.Result
	err     error
	lines   []string
	cancel  context.CancelFunc
}

type (
	readyMsg  struct{ id int }
	resultMsg struct {
		id  int
		res plugin.Result
		err error
	}
	lineMsg struct {
		id   int
		line string
		ch   <-chan string
	}
	endMsg struct{ id int }
)

type model struct {
	width    int
	height   int
	theme    int
	sidebar  bool
	mouse    bool
	seq      int
	conns    []*connection
	active   int
	profiles []profile
	history  []string
	follow   bool
	input    textarea.Model
	vp       viewport.Model
	dlg      dialog
	sel      selection
	clicks   []area
	notice   string
	noticeID int
	comp     *completion
}

type completion struct {
	items []string
	i     int
}

func Run(demo bool) error {
	m := newModel()
	if demo {
		m.seedDemo()
	}
	_, err := tea.NewProgram(m).Run()
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
	m.input.KeyMap.InsertNewline.SetKeys("shift+enter", "ctrl+j")
	m.input.Focus()
	m.vp = viewport.New()
	m.applyTheme()
	return m
}

func (m *model) seedDemo() {
	for _, p := range []profile{
		{name: "local-pg", kind: "postgres", uri: "postgres://app:secret@localhost:5432/shop"},
		{name: "docs", kind: "mongodb", uri: "mongodb://localhost:27017/shop"},
		{name: "cache", kind: "redis", uri: "redis://localhost:6379/0"},
		{name: "broker", kind: "rabbitmq", uri: "amqp://guest:guest@localhost:5672/"},
		{name: "events", kind: "kafka", uri: "localhost:9092"},
		{name: "logs", kind: "loki", uri: "http://localhost:3100"},
	} {
		m.saveProfile(p)
		m.conns[len(m.conns)-1].ready = true
	}
	m.profiles = append(m.profiles,
		profile{name: "staging-pg", kind: "postgres", uri: "postgres://app:secret@staging.internal:5432/shop"},
		profile{name: "prod-cache", kind: "redis", uri: "redis://:secret@cache.prod.internal:6379/0"},
	)
	m.switchTab(0)
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

func (m *model) conn() *connection {
	if m.active < 0 {
		return nil
	}
	return m.conns[m.active]
}

func (m *model) entry(id int) (*connection, *entry) {
	for _, c := range m.conns {
		for _, e := range c.entries {
			if e.id == id {
				return c, e
			}
		}
	}
	return nil, nil
}

func busy(c *connection) (running, live bool) {
	if c == nil {
		return false, false
	}
	for _, e := range c.entries {
		running = running || e.running
		live = live || e.live
	}
	return running, live
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
	case readyMsg:
		for _, c := range m.conns {
			if c.id == msg.id {
				c.ready = true
			}
		}
		return nil
	case resultMsg:
		tab, e := m.entry(msg.id)
		if e == nil {
			return nil
		}
		e.running, e.elapsed, e.res, e.err = false, time.Since(e.at), msg.res, msg.err
		m.follow = tab == m.conn()
		if msg.res.Stream != nil {
			e.live = true
			return wait(e.id, msg.res.Stream)
		}
		e.cancel()
		return nil
	case lineMsg:
		tab, e := m.entry(msg.id)
		if e == nil {
			return nil
		}
		e.lines = append(e.lines, msg.line)
		if len(e.lines) > maxLiveLines {
			e.lines = e.lines[len(e.lines)-maxLiveLines:]
		}
		m.follow = tab == m.conn() && m.vp.AtBottom()
		return wait(msg.id, msg.ch)
	case endMsg:
		if _, e := m.entry(msg.id); e != nil {
			e.live = false
			e.cancel()
		}
		return nil
	case noticeMsg:
		if msg.id == m.noticeID {
			m.notice = ""
		}
		return nil
	case tea.MouseWheelMsg:
		m.vp, _ = m.vp.Update(msg)
		return nil
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		return m.handleMouse(msg.(tea.MouseMsg))
	case tea.KeyPressMsg:
		m.sel = selection{}
		if msg.String() == "ctrl+c" {
			return tea.Quit
		}
		if m.dlg != nil {
			break
		}
		if k := msg.String(); k != "tab" && k != "shift+tab" {
			m.comp = nil
		}
		switch msg.String() {
		case "ctrl+p":
			return open(m.palette())
		case "/":
			if m.input.Value() == "" {
				return open(m.palette())
			}
		case "ctrl+t":
			return open(m.newTab())
		case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
			if i := int(msg.String()[4] - '1'); i < len(m.conns) {
				m.switchTab(i)
			}
			return nil
		case "ctrl+o":
			return open(m.resources())
		case "ctrl+x":
			return open(m.actions())
		case "esc":
			stop(m.conn())
			return nil
		case "tab", "shift+tab":
			step := 1
			if msg.String() == "shift+tab" {
				step = -1
			}
			if cmd, ok := m.complete(step); ok {
				return cmd
			}
			m.cycle(step)
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
			q := strings.TrimSpace(m.input.Value())
			if q == "" {
				return nil
			}
			m.input.Reset()
			return m.run(q)
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

func wait(id int, ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return endMsg{id}
		}
		return lineMsg{id, line, ch}
	}
}

func (m *model) run(raw string) tea.Cmd {
	tab := m.conn()
	if tab == nil {
		return open(m.newTab())
	}
	c, q, err := m.target(raw)
	m.seq++
	e := &entry{id: m.seq, conn: c, at: time.Now(), query: q, err: err}
	tab.entries = append(tab.entries, e)
	m.history = append(m.history, raw)
	m.follow = true
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.running, e.cancel = true, cancel
	id, p := e.id, c.plug
	return func() tea.Msg {
		res, err := p.Query(ctx, q)
		return resultMsg{id, res, err}
	}
}

func (m *model) target(raw string) (*connection, string, error) {
	rest, ok := strings.CutPrefix(raw, "!")
	if !ok {
		return m.conn(), raw, nil
	}
	name, q, _ := strings.Cut(rest, " ")
	q = strings.TrimSpace(q)
	if name == "" || q == "" {
		return nil, raw, errors.New("usage: !<connection> <query>, e.g. !mongo db.users.find()")
	}
	c, err := m.resolve(name)
	if err != nil {
		return nil, raw, err
	}
	return c, q, nil
}

func (m *model) complete(step int) (tea.Cmd, bool) {
	v := m.input.Value()
	if m.comp != nil && v == "!"+m.comp.items[m.comp.i]+" " {
		n := len(m.comp.items)
		m.comp.i = (m.comp.i + step + n) % n
	} else {
		partial, ok := strings.CutPrefix(v, "!")
		if !ok || strings.ContainsAny(partial, " \n") {
			return nil, false
		}
		p := strings.ToLower(partial)
		var items []string
		for _, c := range m.conns {
			if strings.HasPrefix(strings.ToLower(c.name), p) || strings.HasPrefix(c.kind, p) ||
				strings.ToLower(kindOf(c.kind).code) == p {
				items = append(items, c.name)
			}
		}
		if len(items) == 0 {
			return m.flash(fg(m.t().err).Render("no connection matches " + strconv.Quote(partial))), true
		}
		m.comp = &completion{items: items}
		if step < 0 {
			m.comp.i = len(items) - 1
		}
	}
	m.input.SetValue("!" + m.comp.items[m.comp.i] + " ")
	if len(m.comp.items) == 1 {
		return nil, true
	}
	t := m.t()
	var names []string
	for i, n := range m.comp.items {
		if i == m.comp.i {
			names = append(names, fg(t.primary).Bold(true).Render(n))
		} else {
			names = append(names, fg(t.muted).Render(n))
		}
	}
	return m.flash(strings.Join(names, fg(t.muted).Render(" · "))), true
}

func (m *model) resolve(name string) (*connection, error) {
	n := strings.ToLower(name)
	for _, match := range []func(c *connection) bool{
		func(c *connection) bool { return strings.ToLower(c.name) == n },
		func(c *connection) bool { return c.kind == n || strings.ToLower(kindOf(c.kind).code) == n },
		func(c *connection) bool {
			return strings.HasPrefix(strings.ToLower(c.name), n) || strings.HasPrefix(c.kind, n)
		},
	} {
		var hits []*connection
		var names []string
		for _, c := range m.conns {
			if match(c) {
				hits = append(hits, c)
				names = append(names, c.name)
			}
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
		if len(hits) > 1 {
			return nil, fmt.Errorf("%q matches %s, use the connection name", name, strings.Join(names, ", "))
		}
	}
	return nil, fmt.Errorf("no connection matches %q", name)
}

func stop(c *connection) {
	if c == nil {
		return
	}
	for _, e := range c.entries {
		if e.running || e.live {
			e.cancel()
		}
	}
}

func (m *model) cycle(step int) {
	if n := len(m.conns); n > 0 {
		m.switchTab((m.active + step + n) % n)
	}
}

func (m *model) switchTab(i int) {
	if c := m.conn(); c != nil {
		c.draft, c.offset = m.input.Value(), m.vp.YOffset()
		if m.vp.AtBottom() {
			c.offset = -1
		}
	}
	m.active = i
	c := m.conn()
	if c == nil {
		m.input.Reset()
		return
	}
	m.input.SetValue(c.draft)
	m.layout()
	if c.offset < 0 {
		m.vp.GotoBottom()
	} else {
		m.vp.SetYOffset(c.offset)
	}
}

func (m *model) checkName(name string) error {
	switch {
	case name == "":
		return errors.New("give the connection a name")
	case strings.ContainsAny(name, " \t!"):
		return errors.New("use a name without spaces or \"!\" in it")
	}
	for _, p := range m.profiles {
		if strings.EqualFold(p.name, name) {
			return fmt.Errorf("a connection named %q already exists", p.name)
		}
	}
	return nil
}

func (m *model) saveProfile(p profile) tea.Cmd {
	m.profiles = append(m.profiles, p)
	return m.openProfile(p)
}

func (m *model) openProfile(p profile) tea.Cmd {
	for i, c := range m.conns {
		if c.name == p.name {
			m.switchTab(i)
			return nil
		}
	}
	m.seq++
	c := &connection{id: m.seq, name: p.name, kind: p.kind, uri: p.uri, plug: plugin.Mock(p.kind)}
	m.conns = append(m.conns, c)
	m.switchTab(len(m.conns) - 1)
	delay := time.Duration(400+rand.IntN(800)) * time.Millisecond
	return tea.Tick(delay, func(time.Time) tea.Msg { return readyMsg{c.id} })
}

func (m *model) connectForm() dialog {
	return newConnect(m.t(), m.checkName, m.saveProfile)
}

func (m *model) newTab() dialog {
	if len(m.profiles) == 0 {
		return m.connectForm()
	}
	return newList(m.t(), "New tab", false, func() []item {
		items := []item{{label: "+ New connection", hint: "name, type, URI", run: func() tea.Cmd {
			return open(m.connectForm())
		}}}
		for _, p := range m.profiles {
			hint := p.kind
			for _, c := range m.conns {
				if c.name == p.name {
					hint += " · open"
				}
			}
			items = append(items, item{label: p.name, hint: hint, run: func() tea.Cmd {
				return m.openProfile(p)
			}})
		}
		return items
	})
}

func (m *model) closeTab() {
	c := m.conn()
	if c == nil {
		return
	}
	stop(c)
	m.conns = append(m.conns[:m.active], m.conns[m.active+1:]...)
	next := min(m.active, len(m.conns)-1)
	m.active = -1
	m.switchTab(next)
}

func (m *model) palette() dialog {
	return newList(m.t(), "Commands", false, func() []item {
		return []item{
			{label: "New tab (connect)", hint: "ctrl+t", run: func() tea.Cmd {
				return open(m.newTab())
			}},
			{label: "Open resource", hint: "ctrl+o", run: func() tea.Cmd {
				return open(m.resources())
			}},
			{label: "Resource actions", hint: "ctrl+x", run: func() tea.Cmd {
				return open(m.actions())
			}},
			{label: "Query history", run: func() tea.Cmd { return open(m.historyList()) }},
			{label: "Go to tab", hint: "tab · alt+1..9", run: func() tea.Cmd {
				return open(m.switcher())
			}},
			{label: "Stop running in this tab", hint: "esc", run: func() tea.Cmd {
				stop(m.conn())
				return nil
			}},
			{label: "Clear this tab", run: func() tea.Cmd {
				if c := m.conn(); c != nil {
					stop(c)
					c.entries = nil
				}
				return nil
			}},
			{label: "Close tab", danger: true, run: func() tea.Cmd {
				m.closeTab()
				return nil
			}},
			{label: "Settings", run: func() tea.Cmd { return open(m.settings()) }},
			{label: "Toggle sidebar", hint: "ctrl+b", run: func() tea.Cmd {
				m.sidebar = !m.sidebar
				return nil
			}},
			{label: "Help", run: func() tea.Cmd { return open(m.help()) }},
			{label: "Quit", hint: "ctrl+c", run: func() tea.Cmd { return tea.Quit }},
		}
	})
}

func (m *model) resources() dialog {
	c := m.conn()
	if c == nil {
		return m.newTab()
	}
	return newList(m.t(), "Open · "+c.name, false, func() []item {
		var items []item
		for _, r := range c.plug.Resources() {
			items = append(items, item{label: r.Name, hint: r.Kind, run: func() tea.Cmd {
				return m.openResource(c, r)
			}})
		}
		return items
	})
}

func (m *model) openResource(c *connection, r plugin.Resource) tea.Cmd {
	c.current = &r
	return m.run(c.plug.Actions(r)[0].Query)
}

func (m *model) actions() dialog {
	c := m.conn()
	if c == nil || c.current == nil {
		return m.resources()
	}
	r := *c.current
	return newList(m.t(), "Actions · "+r.Name, false, func() []item {
		var items []item
		for _, a := range c.plug.Actions(r) {
			items = append(items, item{label: a.Name, hint: a.Query, danger: a.Danger, run: func() tea.Cmd {
				if a.Danger {
					return open(m.confirm(a.Query))
				}
				return m.run(a.Query)
			}})
		}
		return items
	})
}

func (m *model) confirm(q string) dialog {
	return newList(m.t(), "Are you sure?", false, func() []item {
		return []item{
			{label: "Cancel", run: func() tea.Cmd { return nil }},
			{label: "Run " + q, danger: true, run: func() tea.Cmd { return m.run(q) }},
		}
	})
}

func (m *model) historyList() dialog {
	return newList(m.t(), "Query history", false, func() []item {
		var items []item
		for i := len(m.history) - 1; i >= 0; i-- {
			q := m.history[i]
			first, _, _ := strings.Cut(q, "\n")
			items = append(items, item{label: first, run: func() tea.Cmd {
				m.input.SetValue(q)
				return nil
			}})
		}
		return items
	})
}

func (m *model) switcher() dialog {
	return newList(m.t(), "Go to tab", false, func() []item {
		var items []item
		for i, c := range m.conns {
			items = append(items, item{label: c.name, hint: c.kind, run: func() tea.Cmd {
				m.switchTab(i)
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
			{label: "Theme", hint: m.t().name, run: func() tea.Cmd {
				m.theme = (m.theme + 1) % len(themes)
				m.applyTheme()
				return nil
			}},
			{label: "Sidebar", hint: onOff[m.sidebar], run: func() tea.Cmd {
				m.sidebar = !m.sidebar
				return nil
			}},
			{label: "Mouse: click, select to copy", hint: onOff[m.mouse], run: func() tea.Cmd {
				m.mouse = !m.mouse
				return nil
			}},
		}
	})
}

func (m *model) help() dialog {
	keys := [][2]string{
		{"Run query", "enter"},
		{"Query another connection", "!name query"},
		{"New line", "shift+enter / ctrl+j"},
		{"Commands", "/ or ctrl+p"},
		{"Open resource", "ctrl+o"},
		{"Resource actions", "ctrl+x"},
		{"Stop query or stream", "esc"},
		{"Next / previous tab", "tab / shift+tab"},
		{"Jump to tab", "alt+1..9"},
		{"New tab (connect)", "ctrl+t"},
		{"Toggle sidebar", "ctrl+b"},
		{"Scroll output", "pgup / pgdown"},
		{"Quit", "ctrl+c"},
	}
	return newList(m.t(), "Help", false, func() []item {
		var items []item
		for _, k := range keys {
			items = append(items, item{label: k[0], hint: k[1]})
		}
		return items
	})
}
