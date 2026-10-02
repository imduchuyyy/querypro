package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"querypro/internal/plugin"
)

const (
	connectTimeout = 30 * time.Second
	rpcTimeout     = 30 * time.Second
)

var retryDelay = 5 * time.Second

type connection struct {
	id         int
	name       string
	kind       string
	uri        string
	sess       plugin.Session
	attempt    int
	connecting bool
	err        error
	resources  []plugin.Resource
	current    *plugin.Resource
	entries    []*entry
	draft      string
	offset     int
	resOffset  int
}

type (
	connectedMsg struct {
		id      int
		attempt int
		sess    plugin.Session
		res     []plugin.Resource
		err     error
	}
	resourcesMsg struct {
		id   int
		sess plugin.Session
		res  []plugin.Resource
		err  error
	}
	retryMsg   struct{ id, attempt int }
	actionsMsg struct {
		id   int
		sess plugin.Session
		r    plugin.Resource
		acts []plugin.Action
		err  error
		run  bool
	}
)

var secrets = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)(password\s*=\s*)\S+`), "${1}xxxxx"},
	{regexp.MustCompile(`([^\s:/@]+):[^\s/]*@`), "${1}:xxxxx@"},
}

func (c *connection) safeURI() string {
	if u, err := url.Parse(c.uri); err == nil && u.User != nil {
		return u.Redacted()
	}
	s := c.uri
	for _, r := range secrets {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}

func (m *model) byID(id int) *connection {
	for _, c := range m.conns {
		if c.id == id {
			return c
		}
	}
	return nil
}

func closeLater(s plugin.Session) {
	go func() { _ = s.Close() }()
}

func (m *model) connect(c *connection) tea.Cmd {
	if old := c.sess; old != nil {
		closeLater(old)
	}
	c.attempt++
	c.sess, c.connecting, c.err = nil, true, nil
	host, id, attempt, kind, uri := m.host, c.id, c.attempt, c.kind, c.uri
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		s, err := host.Connect(ctx, kind, uri)
		if err != nil {
			return connectedMsg{id: id, attempt: attempt, err: err}
		}
		res, err := s.Resources(ctx)
		return connectedMsg{id: id, attempt: attempt, sess: s, res: res, err: err}
	}
}

func (m *model) connected(msg connectedMsg) tea.Cmd {
	c := m.byID(msg.id)
	if c == nil || c.attempt != msg.attempt {
		if msg.sess != nil {
			closeLater(msg.sess)
		}
		return nil
	}
	c.connecting = false
	c.sess, c.resources, c.err = msg.sess, msg.res, msg.err
	if c.sess == nil {
		return m.retry(c)
	}
	return nil
}

func (m *model) retry(c *connection) tea.Cmd {
	id, attempt := c.id, c.attempt
	return tea.Tick(retryDelay, func(time.Time) tea.Msg { return retryMsg{id, attempt} })
}

func (m *model) retried(msg retryMsg) tea.Cmd {
	c := m.byID(msg.id)
	if c == nil || c.attempt != msg.attempt || c.sess != nil || c.connecting {
		return nil
	}
	return m.connect(c)
}

func (m *model) refresh(c *connection) tea.Cmd {
	if c == nil || c.sess == nil {
		return m.notConnected(c)
	}
	s, id := c.sess, c.id
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		res, err := s.Resources(ctx)
		return resourcesMsg{id: id, sess: s, res: res, err: err}
	}
}

func (m *model) refreshed(msg resourcesMsg) tea.Cmd {
	c := m.byID(msg.id)
	if c == nil {
		return nil
	}
	if cmd := m.lost(c, msg.sess, msg.err); cmd != nil {
		return cmd
	}
	c.err = msg.err
	if msg.err != nil {
		return m.fail(msg.err)
	}
	c.resources = msg.res
	return m.flash(fg(m.t().success).Render(fmt.Sprintf("✓ %d resources", len(msg.res))))
}

func (m *model) fetchActions(c *connection, r plugin.Resource, run bool) tea.Cmd {
	if c.sess == nil {
		return m.notConnected(c)
	}
	s, id := c.sess, c.id
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		acts, err := s.Actions(ctx, r)
		return actionsMsg{id: id, sess: s, r: r, acts: acts, err: err, run: run}
	}
}

func (m *model) gotActions(msg actionsMsg) tea.Cmd {
	c := m.byID(msg.id)
	if cmd := m.lost(c, msg.sess, msg.err); cmd != nil {
		return cmd
	}
	switch {
	case c == nil || c != m.conn():
		return nil
	case msg.err != nil:
		return m.fail(msg.err)
	case len(msg.acts) == 0:
		return m.fail(errors.New("no actions for " + msg.r.Name))
	case msg.run:
		return m.run(msg.acts[0].Query)
	}
	m.dlg = m.actionList(msg.r, msg.acts)
	return nil
}

func (m *model) openResource(c *connection, r plugin.Resource) tea.Cmd {
	c.current = &r
	return m.fetchActions(c, r, true)
}

func (m *model) showActions() tea.Cmd {
	c := m.conn()
	if c == nil || c.current == nil {
		return open(m.resources())
	}
	return m.fetchActions(c, *c.current, false)
}

func (m *model) notConnected(c *connection) tea.Cmd {
	if c == nil {
		return open(m.newTab())
	}
	return m.fail(fmt.Errorf("%s is not connected, use Reconnect", c.name))
}

func (m *model) fail(err error) tea.Cmd {
	return m.flash(fg(m.t().err).Render("✗ " + err.Error()))
}

func (m *model) lost(c *connection, s plugin.Session, err error) tea.Cmd {
	if c == nil || s == nil || c.sess != s || !errors.Is(err, plugin.ErrDisconnected) {
		return nil
	}
	return tea.Batch(m.fail(fmt.Errorf("%s: %w, reconnecting", c.name, err)), m.connect(c))
}

func (m *model) pluginExited(e plugin.Exit) tea.Cmd {
	var cmds []tea.Cmd
	for _, c := range m.conns {
		if c.kind == e.Kind && (c.sess != nil || c.connecting) {
			if c.sess != nil {
				cmds = append(cmds, m.retry(c))
			}
			c.sess, c.connecting, c.err = nil, false, e.Err
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) closeAll() {
	for _, c := range m.conns {
		stop(c)
		if c.sess != nil {
			_ = c.sess.Close()
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
		if strings.EqualFold(p.Name, name) {
			return fmt.Errorf("a connection named %q already exists", p.Name)
		}
	}
	return nil
}

func (m *model) saveProfile(p profile) tea.Cmd {
	m.profiles = append(m.profiles, p)
	return tea.Batch(m.saveProfiles(), m.openProfile(p))
}

func (m *model) deleteProfile(name string) tea.Cmd {
	for i, p := range m.profiles {
		if p.Name == name {
			m.profiles = append(m.profiles[:i], m.profiles[i+1:]...)
			break
		}
	}
	return m.saveProfiles()
}

func (m *model) openProfile(p profile) tea.Cmd {
	for i, c := range m.conns {
		if c.name == p.Name {
			m.switchTab(i)
			return nil
		}
	}
	m.seq++
	c := &connection{id: m.seq, name: p.Name, kind: p.Kind, uri: p.URI}
	m.conns = append(m.conns, c)
	m.switchTab(len(m.conns) - 1)
	return m.connect(c)
}

func (m *model) closeTab() {
	c := m.conn()
	if c == nil {
		return
	}
	stop(c)
	if c.sess != nil {
		closeLater(c.sess)
	}
	m.conns = append(m.conns[:m.active], m.conns[m.active+1:]...)
	next := min(m.active, len(m.conns)-1)
	m.active = -1
	m.switchTab(next)
}
