package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"querypro/internal/plugin"
)

func (m *model) connectForm() dialog {
	return newConnect(m.t(), "New connection", profile{}, func(name string) error {
		return m.checkName(name, "")
	}, m.saveProfile)
}

func (m *model) editForm(p profile) dialog {
	return newConnect(m.t(), "Edit "+p.Name, p, func(name string) error {
		return m.checkName(name, p.Name)
	}, func(next profile) tea.Cmd { return m.updateProfile(p.Name, next) })
}

func (m *model) editList() dialog {
	return newList(m.t(), "Edit saved connection", false, func() []item {
		var items []item
		for _, p := range m.profiles {
			items = append(items, item{label: p.Name, hint: p.Kind, run: func() tea.Cmd {
				return open(m.editForm(p))
			}})
		}
		return items
	})
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
			{group: "Connection", label: "New tab (connect)", hint: "ctrl+t", run: func() tea.Cmd {
				return open(m.newTab())
			}},
			{group: "Connection", label: "Reconnect", run: func() tea.Cmd {
				if c := m.conn(); c != nil {
					stop(c)
					return m.connect(c)
				}
				return open(m.newTab())
			}},
			{group: "Connection", label: "Edit saved connection", run: func() tea.Cmd {
				return open(m.editList())
			}},
			{group: "Connection", label: "Delete saved connection", danger: true, run: func() tea.Cmd {
				return open(m.deleteList())
			}},
			{group: "Resources", label: "Open resource", hint: "ctrl+o", run: func() tea.Cmd {
				return open(m.resources())
			}},
			{group: "Resources", label: "Resource actions", hint: "ctrl+x", run: m.showActions},
			{group: "Resources", label: "Refresh resources", run: func() tea.Cmd { return m.refresh(m.conn()) }},
			{group: "Query", label: "Query history", run: func() tea.Cmd { return open(m.historyList()) }},
			{group: "Query", label: "Stop running in this tab", hint: "esc", run: func() tea.Cmd {
				stop(m.conn())
				return nil
			}},
			{group: "Query", label: "Clear this tab", run: func() tea.Cmd {
				if c := m.conn(); c != nil {
					stop(c)
					c.entries = nil
				}
				return nil
			}},
			{group: "Tabs", label: "Go to tab", hint: "tab · alt+1..9", run: func() tea.Cmd {
				return open(m.switcher())
			}},
			{group: "Tabs", label: "Close tab", danger: true, run: func() tea.Cmd {
				m.closeTab()
				return nil
			}},
			{group: "Preferences", label: "Settings", run: func() tea.Cmd { return open(m.settings()) }},
			{group: "Preferences", label: "Toggle sidebar", hint: "ctrl+b", run: func() tea.Cmd {
				m.sidebar = !m.sidebar
				return m.saveSettings()
			}},
			{group: "App", label: "Help", run: func() tea.Cmd { return open(m.help()) }},
			{group: "App", label: "Quit", hint: "ctrl+c", run: func() tea.Cmd { return tea.Quit }},
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
				return m.pick(m.conn(), r, a)
			}})
		}
		return items
	})
}

func (m *model) pick(c *connection, r plugin.Resource, a plugin.Action) tea.Cmd {
	if len(a.Params) > 0 {
		return open(newForm(m.t(), a.Name, a.Params, func(values []string) tea.Cmd {
			return m.build(c, r, a, values)
		}))
	}
	return m.runAction(a.Query, a.Danger)
}

func (m *model) runAction(q string, danger bool) tea.Cmd {
	if danger {
		return open(m.confirm("Run "+q, func() tea.Cmd { return m.run(q) }))
	}
	return m.run(q)
}

func (m *model) rowDetail(e *entry, row int) dialog {
	cells := e.res.Rows[row]
	pairs := make([][2]string, len(e.res.Columns))
	for i, c := range e.res.Columns {
		pairs[i] = [2]string{c, ""}
		if i < len(cells) {
			pairs[i][1] = cells[i]
		}
	}
	return &detailDialog{
		title:  fmt.Sprintf("Row %d of %d", row+1, len(e.res.Rows)),
		pairs:  pairs,
		height: func() int { return max(m.height-12, 4) },
		copy:   m.copy,
	}
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
		{"Full row in a popup", "click a table row"},
		{"More table columns", "shift+← →"},
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
