package tui

import (
	tea "charm.land/bubbletea/v2"
)

const (
	profilesFile = "connections.json"
	settingsFile = "settings.json"
	historyFile  = "history.json"
	maxHistory   = 500
)

type profile struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	URI  string `json:"uri"`
}

type settings struct {
	Theme   string `json:"theme"`
	Sidebar bool   `json:"sidebar"`
	Mouse   bool   `json:"mouse"`
}

func (m *model) load() error {
	if m.st == nil {
		return nil
	}
	s := settings{Theme: themes[0].name, Sidebar: true, Mouse: true}
	if err := m.st.Load(settingsFile, &s); err != nil {
		return err
	}
	for i, t := range themes {
		if t.name == s.Theme {
			m.theme = i
		}
	}
	m.sidebar, m.mouse = s.Sidebar, s.Mouse
	m.applyTheme()
	if err := m.st.Load(profilesFile, &m.profiles); err != nil {
		return err
	}
	return m.st.Load(historyFile, &m.history)
}

func (m *model) save(name string, v any) tea.Cmd {
	if m.st == nil {
		return nil
	}
	if err := m.st.Save(name, v); err != nil {
		return m.fail(err)
	}
	return nil
}

func (m *model) saveSettings() tea.Cmd {
	return m.save(settingsFile, settings{Theme: m.t().name, Sidebar: m.sidebar, Mouse: m.mouse})
}

func (m *model) saveProfiles() tea.Cmd {
	return m.save(profilesFile, m.profiles)
}

func (m *model) remember(q string) tea.Cmd {
	if n := len(m.history); n > 0 && m.history[n-1] == q {
		return nil
	}
	m.history = append(m.history, q)
	if len(m.history) > maxHistory {
		m.history = m.history[len(m.history)-maxHistory:]
	}
	return m.save(historyFile, m.history)
}
