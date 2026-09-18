package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel(t *testing.T) {
	m := newModel()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.addConn(connection{name: "a", kind: "postgres", uri: "postgres://u:secret@h/db"})
	m.addConn(connection{name: "b", kind: "redis", uri: "redis://h:6379"})

	if got := m.conns[0].safeURI(); strings.Contains(got, "secret") {
		t.Fatalf("password leaked: %s", got)
	}
	m.cycle(1)
	if m.active != 0 {
		t.Fatalf("cycle wrapped to %d, want 0", m.active)
	}
	m.active = 1
	m.removeActive()
	if m.active != 0 || len(m.conns) != 1 {
		t.Fatalf("after remove: active=%d conns=%d", m.active, len(m.conns))
	}
	m.removeActive()
	if m.active != -1 {
		t.Fatalf("active=%d with no conns", m.active)
	}

	for _, size := range [][2]int{{120, 30}, {60, 10}, {10, 3}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.Update(m.palette())
		m.View()
	}
}
