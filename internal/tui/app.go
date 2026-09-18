package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"querypro/internal/plugin"
	"querypro/internal/store"
)

const (
	sidebarWidth  = 36
	maxInputLines = 8
	maxLiveLines  = 200
)

type backend interface {
	Kinds() []plugin.Kind
	Connect(ctx context.Context, kind, uri string) (plugin.Session, error)
	Exits() <-chan plugin.Exit
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
	view    string
	viewKey string
}

type (
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
	endMsg  struct{ id int }
	exitMsg plugin.Exit
)

type model struct {
	host     backend
	st       *store.Store
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

func Run(host backend, st *store.Store) error {
	m := newModel(host, st)
	if err := m.load(); err != nil {
		return err
	}
	_, err := tea.NewProgram(m).Run()
	m.closeAll()
	return err
}

func newModel(host backend, st *store.Store) *model {
	kinds = host.Kinds()
	m := &model{host: host, st: st, sidebar: true, mouse: true, active: -1}
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

func (m *model) Init() tea.Cmd { return waitExit(m.host.Exits()) }

func waitExit(ch <-chan plugin.Exit) tea.Cmd {
	return func() tea.Msg { return exitMsg(<-ch) }
}

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
	case connectedMsg:
		return m.connected(msg)
	case resourcesMsg:
		return m.refreshed(msg)
	case actionsMsg:
		return m.gotActions(msg)
	case exitMsg:
		m.pluginExited(plugin.Exit(msg))
		return tea.Batch(waitExit(m.host.Exits()), m.flash(fg(m.t().err).Render("✗ "+msg.Err.Error())))
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
			if e.res.Err != nil {
				e.err = e.res.Err()
			}
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
		if cmd, ok := m.key(msg); ok {
			return cmd
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

func (m *model) key(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	m.sel = selection{}
	if msg.String() == "ctrl+c" {
		return tea.Quit, true
	}
	if m.dlg != nil {
		return nil, false
	}
	if k := msg.String(); k != "tab" && k != "shift+tab" {
		m.comp = nil
	}
	switch msg.String() {
	case "ctrl+p":
		return open(m.palette()), true
	case "/":
		if m.input.Value() == "" {
			return open(m.palette()), true
		}
	case "ctrl+t":
		return open(m.newTab()), true
	case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
		if i := int(msg.String()[4] - '1'); i < len(m.conns) {
			m.switchTab(i)
		}
		return nil, true
	case "ctrl+o":
		return open(m.resources()), true
	case "ctrl+x":
		return m.showActions(), true
	case "esc":
		stop(m.conn())
		return nil, true
	case "tab", "shift+tab":
		step := 1
		if msg.String() == "shift+tab" {
			step = -1
		}
		if cmd, ok := m.complete(step); ok {
			return cmd, true
		}
		m.cycle(step)
		return nil, true
	case "ctrl+b":
		m.sidebar = !m.sidebar
		return m.saveSettings(), true
	case "pgup":
		m.vp.PageUp()
		return nil, true
	case "pgdown":
		m.vp.PageDown()
		return nil, true
	case "enter":
		q := strings.TrimSpace(m.input.Value())
		if q == "" {
			return nil, true
		}
		m.input.Reset()
		return m.run(q), true
	}
	return nil, false
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
	m.follow = true
	save := m.remember(raw)
	if err != nil {
		return save
	}
	if c.sess == nil {
		if c.connecting {
			e.err = fmt.Errorf("%s is still connecting, try again in a moment", c.name)
			return save
		}
		e.err = fmt.Errorf("%s is not connected, reconnecting now", c.name)
		return tea.Batch(save, m.connect(c))
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.running, e.cancel = true, cancel
	id, s := e.id, c.sess
	return tea.Batch(save, func() tea.Msg {
		res, err := s.Query(ctx, q)
		return resultMsg{id, res, err}
	})
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
				strings.ToLower(kindOf(c.kind).Code) == p {
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
		func(c *connection) bool { return c.kind == n || strings.ToLower(kindOf(c.kind).Code) == n },
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
