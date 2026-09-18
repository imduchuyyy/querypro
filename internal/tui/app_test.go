package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel(t *testing.T) {
	m := newModel()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.saveProfile(profile{name: "a", kind: "postgres", uri: "postgres://u:secret@h/db"})
	m.saveProfile(profile{name: "b", kind: "redis", uri: "redis://h:6379"})

	if got := m.conns[0].safeURI(); strings.Contains(got, "secret") {
		t.Fatalf("password leaked: %s", got)
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
	m := newModel()
	m.seedDemo()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	m.Update(m.run("SELECT * FROM users LIMIT 2;")())
	e := m.conns[0].entries[0]
	if e.running || e.err != nil || len(e.res.Rows) != 2 {
		t.Fatalf("query: running=%v err=%v rows=%d", e.running, e.err, len(e.res.Rows))
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
	if s.live {
		t.Fatal("stream still live after stop")
	}
	m.View()
}

func TestMouse(t *testing.T) {
	writeClipboard = func(string) error { return nil }
	m := newModel()
	m.seedDemo()
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
	m.Update(tea.MouseClickMsg{X: 4, Y: tablineHeight + 9, Button: tea.MouseLeft})
	if c := m.conn(); c.current == nil || c.current.Name != "users" {
		t.Fatalf("click on first resource opened %v", c.current)
	}

	m.switchTab(0)
	m.Update(m.run("SELECT * FROM users LIMIT 1;")())
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

func TestTargetedQuery(t *testing.T) {
	m := newModel()
	m.seedDemo()
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

	m.Update(m.run("!mongo db.users.find()")())
	e := m.conn().entries[0]
	if m.active != 0 || e.conn.name != "docs" || e.query != "db.users.find()" || e.err != nil {
		t.Fatalf("active=%d conn=%s query=%q err=%v", m.active, e.conn.name, e.query, e.err)
	}
	if m.run("!mongo"); m.conn().entries[1].err == nil {
		t.Fatal("missing query should fail")
	}
}

func TestComplete(t *testing.T) {
	m := newModel()
	m.seedDemo()
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
	m := newModel()
	m.seedDemo()
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
	m.openProfile(m.profiles[6])
	if len(m.conns) != 7 || m.conn().name != "staging-pg" {
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
	if next, _ := d.update(tea.KeyPressMsg{Code: tea.KeyEnter}); next != nil {
		t.Fatal("valid name should close the form")
	}
	if m.conn().name != "new-one" || m.profiles[len(m.profiles)-1].name != "new-one" {
		t.Fatalf("saved %q, active tab %q", m.profiles[len(m.profiles)-1].name, m.conn().name)
	}
}
