package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type dialog interface {
	update(msg tea.Msg) (dialog, tea.Cmd)
	view(t theme, width int) string
}

func open(d dialog) tea.Cmd {
	return func() tea.Msg { return d }
}

func titled(border color.Color, width int, title, body string, pad int) string {
	b := lipgloss.RoundedBorder()
	box := lipgloss.NewStyle().
		Border(b).BorderTop(false).BorderForeground(border).
		Padding(0, pad).Width(width)
	fill := max(width-lipgloss.Width(title)-5, 0)
	top := fg(border).Render(b.TopLeft+b.Top+" ") + title +
		fg(border).Render(" "+strings.Repeat(b.Top, fill)+b.TopRight)
	return top + "\n" + box.Render(body)
}

func frame(t theme, width int, title, body, hint string) string {
	body = "\n" + body + "\n\n" + fg(t.muted).Render(hint) + "\n"
	return titled(t.border, width, pill(title, t.accent, t.bg), body, 2)
}

func spread(left, right string, width int) string {
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func newInput(t theme, placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = placeholder
	s := textinput.DefaultDarkStyles()
	s.Focused.Prompt = fg(t.primary)
	s.Blurred.Prompt = fg(t.muted)
	s.Focused.Text = fg(t.text)
	s.Blurred.Text = fg(t.muted)
	s.Focused.Placeholder = fg(t.muted)
	s.Blurred.Placeholder = fg(t.muted)
	s.Cursor.Color = t.primary
	in.SetStyles(s)
	return in
}

type item struct {
	label  string
	hint   string
	danger bool
	run    func() tea.Cmd
}

type listDialog struct {
	title  string
	items  func() []item
	keep   bool
	search textinput.Model
	cursor int
}

func newList(t theme, title string, keep bool, items func() []item) *listDialog {
	d := &listDialog{title: title, items: items, keep: keep}
	d.search = newInput(t, "search")
	d.search.Focus()
	return d
}

func (d *listDialog) visible() []item {
	q := strings.ToLower(d.search.Value())
	var out []item
	for _, it := range d.items() {
		if strings.Contains(strings.ToLower(it.label), q) {
			out = append(out, it)
		}
	}
	return out
}

func (d *listDialog) update(msg tea.Msg) (dialog, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		d.search, cmd = d.search.Update(msg)
		return d, cmd
	}
	items := d.visible()
	switch k.String() {
	case "esc":
		return nil, nil
	case "up", "ctrl+p":
		d.cursor = max(d.cursor-1, 0)
	case "down", "ctrl+n":
		d.cursor = min(d.cursor+1, max(len(items)-1, 0))
	case "enter":
		if d.cursor >= len(items) || items[d.cursor].run == nil {
			return d, nil
		}
		cmd := items[d.cursor].run()
		if d.keep {
			return d, cmd
		}
		return nil, cmd
	default:
		var cmd tea.Cmd
		d.search, cmd = d.search.Update(msg)
		d.cursor = 0
		return d, cmd
	}
	return d, nil
}

func (d *listDialog) view(t theme, width int) string {
	inner := width - 6
	d.search.SetWidth(max(inner-2, 1))
	rows := []string{d.search.View(), ""}
	items := d.visible()
	if len(items) == 0 {
		rows = append(rows, fg(t.muted).Render("no results"))
	}
	for i, it := range items {
		color := t.text
		if it.danger {
			color = t.err
		}
		mark, label := "  ", fg(color).Render(it.label)
		if i == d.cursor {
			if !it.danger {
				color = t.primary
			}
			mark = fg(color).Render("❯ ")
			label = fg(color).Bold(true).Render(it.label)
		}
		hint := lipgloss.NewStyle().MaxWidth(max(inner-lipgloss.Width(label)-4, 0)).
			Render(fg(t.muted).Render(it.hint))
		rows = append(rows, mark+spread(label, hint, inner-2))
	}
	return frame(t, width, d.title, strings.Join(rows, "\n"),
		"↑↓ select · enter run · esc close")
}

type connectDialog struct {
	kind   int
	focus  int
	err    string
	name   textinput.Model
	uri    textinput.Model
	check  func(name string) error
	onSave func(profile) tea.Cmd
}

func newConnect(t theme, check func(string) error, onSave func(profile) tea.Cmd) *connectDialog {
	d := &connectDialog{check: check, onSave: onSave}
	d.name = newInput(t, "e.g. local-pg, prod-mongo")
	d.uri = newInput(t, kinds[0].URI)
	d.name.Focus()
	return d
}

func (d *connectDialog) setFocus(i int) tea.Cmd {
	d.focus = (i + 3) % 3
	d.name.Blur()
	d.uri.Blur()
	switch d.focus {
	case 0:
		return d.name.Focus()
	case 2:
		return d.uri.Focus()
	}
	return nil
}

func (d *connectDialog) update(msg tea.Msg) (dialog, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			return nil, nil
		case "tab", "down":
			return d, d.setFocus(d.focus + 1)
		case "shift+tab", "up":
			return d, d.setFocus(d.focus - 1)
		case "enter":
			name := strings.TrimSpace(d.name.Value())
			if err := d.check(name); err != nil {
				d.err = err.Error()
				return d, d.setFocus(0)
			}
			return nil, d.onSave(profile{
				Name: name,
				Kind: kinds[d.kind].Name,
				URI:  or(strings.TrimSpace(d.uri.Value()), d.uri.Placeholder),
			})
		}
		if d.focus == 1 {
			switch k.String() {
			case "left", "h":
				d.kind = (d.kind - 1 + len(kinds)) % len(kinds)
			case "right", "l":
				d.kind = (d.kind + 1) % len(kinds)
			}
			d.uri.Placeholder = kinds[d.kind].URI
			return d, nil
		}
		d.err = ""
	}
	var cmd tea.Cmd
	switch d.focus {
	case 0:
		d.name, cmd = d.name.Update(msg)
	case 2:
		d.uri, cmd = d.uri.Update(msg)
	}
	return d, cmd
}

func (d *connectDialog) view(t theme, width int) string {
	inner := width - 6
	d.name.SetWidth(max(inner-2, 1))
	d.uri.SetWidth(max(inner-2, 1))
	label := func(s string, i int) string {
		if d.focus == i {
			return fg(t.primary).Bold(true).Render(s)
		}
		return fg(t.muted).Render(s)
	}
	var types []string
	for i, k := range kinds {
		types = append(types, badge(t, k.Name, i == d.kind))
	}
	typeRow := strings.Join(types, " ") + "  " + fg(t.text).Render(kinds[d.kind].Name)
	errLine := ""
	if d.err != "" {
		errLine = fg(t.err).Render("✗ " + d.err)
	}
	body := strings.Join([]string{
		label("Name", 0) + fg(t.muted).Render("  used in the tab and in !name queries"),
		d.name.View(),
		"",
		label("Type", 1) + fg(t.muted).Render("  ← →"),
		typeRow,
		"",
		label("URI", 2),
		d.uri.View(),
		"",
		errLine,
	}, "\n")
	return frame(t, width, "New connection", body, "tab next field · enter save · esc close")
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
