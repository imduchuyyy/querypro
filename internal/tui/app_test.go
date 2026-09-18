package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"querypro/internal/plugin"
	"querypro/internal/store"
)

var testKinds = []plugin.Kind{
	{Name: "postgres", Code: "PG", Color: "#5b9bd5", URI: "postgres://localhost", Placeholder: "SELECT 1;"},
	{Name: "mongodb", Code: "MG", Color: "#4db33d", URI: "mongodb://localhost", Placeholder: "db.x.find()"},
	{Name: "redis", Code: "RD", Color: "#e5534b", URI: "redis://localhost", Placeholder: "GET k"},
	{Name: "rabbitmq", Code: "MQ", Color: "#ff8a3d", URI: "amqp://localhost", Placeholder: "queues"},
	{Name: "kafka", Code: "KF", Color: "#b4bccb", URI: "kafka://localhost", Placeholder: "topics"},
	{Name: "loki", Code: "LK", Color: "#f2cc0c", URI: "http://localhost", Placeholder: "{app=\"x\"}"},
}

type fakeHost struct{}

func (fakeHost) Kinds() []plugin.Kind      { return testKinds }
func (fakeHost) Exits() <-chan plugin.Exit { return nil }

func (fakeHost) Connect(_ context.Context, kind, uri string) (plugin.Session, error) {
	if strings.Contains(uri, "down") {
		return nil, errors.New("connection refused")
	}
	return &fakeSession{}, nil
}

type fakeSession struct{ closed bool }

func (s *fakeSession) Server() string { return "fake 1.0" }
func (s *fakeSession) Close() error   { s.closed = true; return nil }

func (s *fakeSession) Resources(context.Context) ([]plugin.Resource, error) {
	return []plugin.Resource{{Kind: "table", Name: "users"}, {Kind: "table", Name: "orders"}}, nil
}

func (s *fakeSession) Actions(_ context.Context, r plugin.Resource) ([]plugin.Action, error) {
	return []plugin.Action{
		{Name: "Preview", Query: "SELECT * FROM " + r.Name + " LIMIT 2;"},
		{Name: "Drop", Query: "DROP TABLE " + r.Name + ";", Danger: true},
	}, nil
}

func (s *fakeSession) Query(ctx context.Context, q string) (plugin.Result, error) {
	switch {
	case strings.HasPrefix(q, "consume"):
		ch := make(chan string)
		go func() {
			defer close(ch)
			for i := 0; ; i++ {
				select {
				case ch <- fmt.Sprint("msg ", i):
				case <-ctx.Done():
					return
				}
			}
		}()
		return plugin.Result{Stream: ch, Summary: "consuming", Err: func() error { return nil }}, nil
	case strings.HasPrefix(q, "fail"):
		return plugin.Result{}, errors.New("boom")
	}
	return plugin.Result{Columns: []string{"id", "q"}, Rows: [][]string{{"1", q}, {"2", q}}, Summary: "2 rows"}, nil
}

func do(m *model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			do(m, c)
		}
	case noticeMsg:
	default:
		m.Update(msg)
	}
}

func seed(t *testing.T) *model {
	t.Helper()
	m := newModel(fakeHost{}, nil)
	for _, p := range []profile{
		{Name: "local-pg", Kind: "postgres", URI: "postgres://app:secret@localhost:5432/shop"},
		{Name: "docs", Kind: "mongodb", URI: "mongodb://localhost:27017/shop"},
		{Name: "cache", Kind: "redis", URI: "redis://localhost:6379/0"},
		{Name: "broker", Kind: "rabbitmq", URI: "amqp://guest:guest@localhost:5672/"},
		{Name: "events", Kind: "kafka", URI: "localhost:9092"},
		{Name: "logs", Kind: "loki", URI: "http://localhost:3100"},
	} {
		do(m, m.saveProfile(p))
	}
	m.profiles = append(m.profiles,
		profile{Name: "staging-pg", Kind: "postgres", URI: "postgres://app:secret@staging.internal:5432/shop"},
		profile{Name: "prod-cache", Kind: "redis", URI: "redis://:secret@cache.prod.internal:6379/0"},
	)
	m.switchTab(0)
	return m
}

func TestModel(t *testing.T) {
	m := newModel(fakeHost{}, nil)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	do(m, m.saveProfile(profile{Name: "a", Kind: "postgres", URI: "postgres://u:secret@h/db"}))
	do(m, m.saveProfile(profile{Name: "b", Kind: "redis", URI: "redis://h:6379"}))

	if got := m.conns[0].safeURI(); strings.Contains(got, "secret") {
		t.Fatalf("password leaked: %s", got)
	}
	if c := m.conns[0]; c.sess == nil || len(c.resources) != 2 || c.connecting {
		t.Fatalf("not connected: sess=%v resources=%d", c.sess, len(c.resources))
	}
	m.cycle(1)
	if m.active != 0 {
		t.Fatalf("cycle wrapped to %d, want 0", m.active)
	}
	m.switchTab(1)
	m.closeTab()
	if m.active != 0 || len(m.conns) != 1 {
		t.Fatalf("after close: active=%d conns=%d", m.active, len(m.conns))
	}
	m.closeTab()
	if m.active != -1 {
		t.Fatalf("active=%d with no conns", m.active)
	}

	for _, size := range [][2]int{{120, 30}, {60, 10}, {10, 3}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.Update(m.palette())
		m.View()
	}
}

func TestQueryAndStream(t *testing.T) {
	m := seed(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	do(m, m.run("SELECT * FROM users LIMIT 2;"))
	e := m.conns[0].entries[0]
	if e.running || e.err != nil || len(e.res.Rows) != 2 {
		t.Fatalf("query: running=%v err=%v rows=%d", e.running, e.err, len(e.res.Rows))
	}
	if m.history[len(m.history)-1] != "SELECT * FROM users LIMIT 2;" {
		t.Fatalf("history not recorded: %v", m.history)
	}

	m.switchTab(3)
	_, next := m.Update(m.run("consume emails")())
	_, next = m.Update(next())
	s := m.conns[3].entries[0]
	if !s.live || len(s.lines) != 1 {
		t.Fatalf("stream: live=%v lines=%d", s.live, len(s.lines))
	}
	m.switchTab(0)
	if _, live := busy(m.conns[3]); !live {
		t.Fatal("stream should keep running in a background tab")
	}
	stop(m.conns[3])
	for next != nil {
		_, next = m.Update(next())
	}
	if s.live || s.err != nil {
		t.Fatalf("stream after stop: live=%v err=%v", s.live, s.err)
	}

	do(m, m.run("fail now"))
	if e := m.conns[0].entries[1]; e.err == nil || e.err.Error() != "boom" {
		t.Fatalf("failed query err=%v", e.err)
	}
	m.View()
}

func TestMouse(t *testing.T) {
	writeClipboard = func(string) error { return nil }
	m := seed(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.View()

	var tabs []area
	for _, a := range m.clicks {
		if a.y == 0 {
			tabs = append(tabs, a)
		}
	}
	m.Update(tea.MouseClickMsg{X: tabs[1].x0 + 1, Y: 0, Button: tea.MouseLeft})
	if m.active != 1 {
		t.Fatalf("click on second tab: active=%d", m.active)
	}
	m.View()
	_, cmd := m.Update(tea.MouseClickMsg{X: 4, Y: tablineHeight + 9, Button: tea.MouseLeft})
	do(m, cmd)
	c := m.conn()
	if c.current == nil || c.current.Name != "users" {
		t.Fatalf("click on first resource opened %v", c.current)
	}
	if len(c.entries) != 1 || c.entries[0].query != "SELECT * FROM users LIMIT 2;" {
		t.Fatalf("opening a resource should run its first action, entries=%d", len(c.entries))
	}

	m.switchTab(0)
	do(m, m.run("SELECT * FROM users LIMIT 1;"))
	m.View()
	x, y := m.origin()
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 30, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: x + 30, Y: y, Button: tea.MouseLeft})
	if !strings.Contains(m.notice, "copied") {
		t.Fatalf("no copy notice, got %q", m.notice)
	}
	if got := m.selectedText(); !strings.HasPrefix(got, "❯ SELECT * FROM users") {
		t.Fatalf("selected %q", got)
	}
}

func TestActions(t *testing.T) {
	m := seed(t)
	do(m, m.showActions())
	if _, ok := m.dlg.(*listDialog); !ok || m.dlg.(*listDialog).title != "Open · local-pg" {
		t.Fatalf("without a resource ctrl+x should list resources, got %#v", m.dlg)
	}
	m.dlg = nil
	c := m.conn()
	c.current = &c.resources[1]
	do(m, m.showActions())
	d, ok := m.dlg.(*listDialog)
	if !ok || d.title != "Actions · orders" || len(d.items()) != 2 || !d.items()[1].danger {
		t.Fatalf("actions dialog: %#v", m.dlg)
	}
	d.cursor = 1
	next, cmd := d.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next != nil {
		t.Fatal("list should close after choosing")
	}
	do(m, cmd)
	confirm, ok := m.dlg.(*listDialog)
	if !ok || confirm.title != "Are you sure?" || len(c.entries) != 0 {
		t.Fatalf("danger action should ask first, dlg=%#v entries=%d", m.dlg, len(c.entries))
	}
	confirm.cursor = 1
	_, cmd = confirm.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	do(m, cmd)
	if len(c.entries) != 1 || c.entries[0].query != "DROP TABLE orders;" {
		t.Fatalf("confirmed action did not run, entries=%d", len(c.entries))
	}
}

func TestConnectionLifecycle(t *testing.T) {
	m := seed(t)
	do(m, m.saveProfile(profile{Name: "broken", Kind: "postgres", URI: "postgres://down"}))
	c := m.conn()
	if c.sess != nil || c.connecting || c.err == nil {
		t.Fatalf("failed connect: sess=%v connecting=%v err=%v", c.sess, c.connecting, c.err)
	}
	cmd := m.run("SELECT 1;")
	if e := c.entries[0]; e.err == nil || !strings.Contains(e.err.Error(), "reconnecting") || !c.connecting {
		t.Fatalf("query while disconnected: err=%v connecting=%v", e.err, c.connecting)
	}
	do(m, cmd)

	old := m.conns[0].sess.(*fakeSession)
	m.Update(exitMsg{Kind: "postgres", Err: errors.New("postgres plugin stopped")})
	if m.conns[0].sess != nil || m.conns[0].err == nil || m.conns[1].sess == nil {
		t.Fatal("plugin exit should disconnect only its own tabs")
	}
	m.switchTab(0)
	do(m, m.connect(m.conns[0]))
	if m.conns[0].sess == nil || m.conns[0].sess == plugin.Session(old) {
		t.Fatal("reconnect should open a new session")
	}

	stale := m.connect(m.conns[0])
	do(m, m.connect(m.conns[0]))
	fresh := m.conns[0].sess
	do(m, stale)
	if m.conns[0].sess != fresh {
		t.Fatal("a stale connect result replaced a newer session")
	}

	s := m.conns[1].sess.(*fakeSession)
	m.closeAll()
	if !s.closed {
		t.Fatal("closeAll should close sessions")
	}
}

func TestTargetedQuery(t *testing.T) {
	m := seed(t)
	for name, want := range map[string]string{
		"docs": "docs", "mongo": "docs", "MG": "docs", "postgres": "local-pg",
		"pg": "local-pg", "cache": "cache", "loki": "logs", "ev": "events",
	} {
		c, err := m.resolve(name)
		if err != nil || c.name != want {
			t.Errorf("resolve(%q) = %v, %v; want %s", name, c, err, want)
		}
	}
	for _, name := range []string{"nope", "l"} {
		if _, err := m.resolve(name); err == nil {
			t.Errorf("resolve(%q) should fail", name)
		}
	}

	do(m, m.run("!mongo db.users.find()"))
	e := m.conn().entries[0]
	if m.active != 0 || e.conn.name != "docs" || e.query != "db.users.find()" || e.err != nil {
		t.Fatalf("active=%d conn=%s query=%q err=%v", m.active, e.conn.name, e.query, e.err)
	}
	if m.run("!mongo"); m.conn().entries[1].err == nil {
		t.Fatal("missing query should fail")
	}
}

func TestComplete(t *testing.T) {
	m := seed(t)
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	for _, c := range []struct {
		typed string
		tabs  int
		want  string
	}{
		{"!mon", 1, "!docs "},
		{"!ca", 1, "!cache "},
		{"!l", 1, "!local-pg "},
		{"!l", 2, "!logs "},
		{"!l", 3, "!local-pg "},
		{"!MG", 1, "!docs "},
		{"!", 2, "!docs "},
	} {
		m.input.SetValue(c.typed)
		m.comp = nil
		for range c.tabs {
			m.Update(tab)
		}
		if got := m.input.Value(); got != c.want {
			t.Errorf("%q + %d tab = %q, want %q", c.typed, c.tabs, got, c.want)
		}
	}

	m.input.SetValue("!zzz")
	m.Update(tab)
	if m.input.Value() != "!zzz" || m.active != 0 {
		t.Fatalf("no match changed input %q or active %d", m.input.Value(), m.active)
	}
	m.input.SetValue("")
	m.Update(tab)
	if m.active != 1 {
		t.Fatalf("tab on empty input should switch connection, active=%d", m.active)
	}
}

func TestNewTabFlow(t *testing.T) {
	m := seed(t)
	for name, ok := range map[string]bool{
		"": false, "has space": false, "!x": false, "DOCS": false, "staging-pg": false, "fresh": true,
	} {
		if err := m.checkName(name); (err == nil) != ok {
			t.Errorf("checkName(%q) = %v", name, err)
		}
	}

	m.openProfile(m.profiles[1])
	if len(m.conns) != 6 || m.active != 1 {
		t.Fatalf("reopening an open profile should switch: tabs=%d active=%d", len(m.conns), m.active)
	}
	do(m, m.openProfile(m.profiles[6]))
	if len(m.conns) != 7 || m.conn().name != "staging-pg" || m.conn().sess == nil {
		t.Fatalf("saved profile should open a tab: tabs=%d active=%s", len(m.conns), m.conn().name)
	}
	m.closeTab()
	if len(m.profiles) != 8 {
		t.Fatalf("closing a tab must keep the profile, profiles=%d", len(m.profiles))
	}

	d := m.connectForm().(*connectDialog)
	next, _ := d.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next == nil || d.err == "" {
		t.Fatal("empty name should keep the form open with an error")
	}
	d.name.SetValue("new-one")
	next, cmd := d.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next != nil {
		t.Fatal("valid name should close the form")
	}
	do(m, cmd)
	if m.conn().name != "new-one" || m.profiles[len(m.profiles)-1].Name != "new-one" || m.conn().sess == nil {
		t.Fatalf("saved %q, active tab %q", m.profiles[len(m.profiles)-1].Name, m.conn().name)
	}

	do(m, m.deleteProfile("new-one"))
	if len(m.profiles) != 8 || m.conn().name != "new-one" {
		t.Fatalf("delete should drop the profile but keep the tab, profiles=%d", len(m.profiles))
	}
}

func TestPersistence(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(fakeHost{}, st)
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	do(m, m.saveProfile(profile{Name: "pg", Kind: "postgres", URI: "postgres://u:p@h/db"}))
	do(m, m.run("SELECT 1;"))
	do(m, m.run("SELECT 1;"))
	m.theme = 1
	m.sidebar = false
	do(m, m.saveSettings())

	again := newModel(fakeHost{}, st)
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	if len(again.profiles) != 1 || again.profiles[0] != m.profiles[0] {
		t.Fatalf("profiles %v", again.profiles)
	}
	if len(again.history) != 1 || again.history[0] != "SELECT 1;" {
		t.Fatalf("history %v", again.history)
	}
	if again.theme != 1 || again.sidebar || !again.mouse {
		t.Fatalf("settings theme=%d sidebar=%v mouse=%v", again.theme, again.sidebar, again.mouse)
	}
}
