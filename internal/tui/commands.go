package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"querypro/internal/plugin"
)

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
			hint := p.Kind
			for _, c := range m.conns {
				if c.name == p.Name {
					hint += " · open"
				}
			}
			items = append(items, item{label: p.Name, hint: hint, run: func() tea.Cmd {
				return m.openProfile(p)
			}})
		}
		return items
	})
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
			{label: "Resource actions", hint: "ctrl+x", run: m.showActions},
			{label: "Refresh resources", run: func() tea.Cmd { return m.refresh(m.conn()) }},
			{label: "Reconnect", run: func() tea.Cmd {
				if c := m.conn(); c != nil {
					stop(c)
					return m.connect(c)
				}
				return open(m.newTab())
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
			{label: "Delete saved connection", danger: true, run: func() tea.Cmd {
				return open(m.deleteList())
			}},
			{label: "Settings", run: func() tea.Cmd { return open(m.settings()) }},
			{label: "Toggle sidebar", hint: "ctrl+b", run: func() tea.Cmd {
				m.sidebar = !m.sidebar
				return m.saveSettings()
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
		for _, r := range c.resources {
			items = append(items, item{label: r.Name, hint: r.Kind, run: func() tea.Cmd {
				return m.openResource(c, r)
			}})
		}
		return items
	})
}

func (m *model) actionList(r plugin.Resource, acts []plugin.Action) dialog {
	return newList(m.t(), "Actions · "+r.Name, false, func() []item {
		var items []item
		for _, a := range acts {
			items = append(items, item{label: a.Name, hint: a.Query, danger: a.Danger, run: func() tea.Cmd {
				if a.Danger {
					return open(m.confirm("Run "+a.Query, func() tea.Cmd { return m.run(a.Query) }))
				}
				return m.run(a.Query)
			}})
		}
		return items
	})
}

func (m *model) confirm(label string, yes func() tea.Cmd) dialog {
	return newList(m.t(), "Are you sure?", false, func() []item {
		return []item{
			{label: "Cancel", run: func() tea.Cmd { return nil }},
			{label: label, danger: true, run: yes},
		}
	})
}

func (m *model) deleteList() dialog {
	return newList(m.t(), "Delete saved connection", false, func() []item {
		var items []item
		for _, p := range m.profiles {
			items = append(items, item{label: p.Name, hint: p.Kind, danger: true, run: func() tea.Cmd {
				return open(m.confirm("Delete "+p.Name, func() tea.Cmd { return m.deleteProfile(p.Name) }))
			}})
		}
		return items
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
				return m.saveSettings()
			}},
			{label: "Sidebar", hint: onOff[m.sidebar], run: func() tea.Cmd {
				m.sidebar = !m.sidebar
				return m.saveSettings()
			}},
			{label: "Mouse: click, select to copy", hint: onOff[m.mouse], run: func() tea.Cmd {
				m.mouse = !m.mouse
				return m.saveSettings()
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
