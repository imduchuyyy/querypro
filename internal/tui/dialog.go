package tui

import (
	"cmp"
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"querypro/internal/plugin"
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
	group  string
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
	offset int
	height int
}

func newList(t theme, title string, keep bool, items func() []item) *listDialog {
	d := &listDialog{title: title, items: items, keep: keep}
	d.search = newInput(t, "search")
	d.search.Focus()
	return d
}

func (d *listDialog) visible() []item {
	q := strings.TrimSpace(d.search.Value())
	if q == "" {
		return d.items()
	}
	type hit struct {
		it    item
		score int
	}
	var hits []hit
	for _, it := range d.items() {
		if s, ok := fuzzy(q, it.label); ok {
			hits = append(hits, hit{it, s})
		} else if s, ok := fuzzy(q, it.group+" "+it.label); ok {
			hits = append(hits, hit{it, s - 10})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(b.score-a.score, len(a.it.label)-len(b.it.label))
	})
	out := make([]item, len(hits))
	for i, h := range hits {
		out[i] = h.it
	}
	return out
}

func fuzzy(query, s string) (int, bool) {
	q, r := []rune(strings.ToLower(query)), []rune(strings.ToLower(s))
	score, j, prev := 0, 0, -1
	for i := 0; i < len(r) && j < len(q); i++ {
		if r[i] != q[j] {
			continue
		}
		switch {
		case i == prev+1:
			score += 3
		case i == 0 || strings.ContainsRune(" _-./:", r[i-1]):
			score += 2
		default:
			score -= min(i-prev, 5)
		}
		prev, j = i, j+1
	}
	if strings.Contains(string(r), string(q)) {
		score += 10
	}
	return score, j == len(q)
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
	case "pgup":
		d.cursor = max(d.cursor-10, 0)
	case "pgdown":
		d.cursor = min(d.cursor+10, max(len(items)-1, 0))
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
	d.cursor = min(d.cursor, max(len(items)-1, 0))
	d.offset = min(d.offset, d.cursor)
	for d.offset < d.cursor && d.lines(items, d.offset, d.cursor) > d.budget() {
		d.offset++
	}
	used, last := 0, d.offset-1
	for i := d.offset; i < len(items); i++ {
		it := items[i]
		if used += d.lines(items, i, i); used > d.budget() {
			break
		}
		last = i
		if d.header(items, i) {
			rows = append(rows, fg(t.muted).Bold(true).Render(strings.ToUpper(it.group)))
		}
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
	hint := "↑↓ select · enter run · esc close"
	if d.offset > 0 || last < len(items)-1 {
		hint = fmt.Sprintf("%d/%d · ", d.cursor+1, len(items)) + hint
	}
	return frame(t, width, d.title, strings.Join(rows, "\n"), hint)
}

func (d *listDialog) budget() int {
	return cmp.Or(d.height, math.MaxInt)
}

func (d *listDialog) header(items []item, i int) bool {
	g := items[i].group
	return d.search.Value() == "" && g != "" && (i == d.offset || items[i-1].group != g)
}

func (d *listDialog) lines(items []item, from, to int) int {
	n := 0
	for i := from; i <= to; i++ {
		n++
		if d.header(items, i) {
			n++
		}
	}
	return n
}

type detailDialog struct {
	title  string
	pairs  [][2]string
	offset int
	height func() int
	copy   func(string) tea.Cmd
}

func (d *detailDialog) text() string {
	var lines []string
	for _, p := range d.pairs {
		lines = append(lines, p[0]+": "+p[1])
	}
	return strings.Join(lines, "\n")
}

func (d *detailDialog) update(msg tea.Msg) (dialog, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "esc", "enter", "q":
		return nil, nil
	case "up", "k":
		d.offset--
	case "down", "j":
		d.offset++
	case "pgup":
		d.offset -= 10
	case "pgdown":
		d.offset += 10
	case "c":
		return d, d.copy(d.text())
	}
	return d, nil
}

func (d *detailDialog) view(t theme, width int) string {
	inner := width - 6
	var lines []string
	for _, p := range d.pairs {
		lines = append(lines, fg(t.accent).Bold(true).Render(p[0]))
		lines = append(lines, strings.Split(textView(t, or(p[1], "(empty)"), inner), "\n")...)
		lines = append(lines, "")
	}
	hint := "c copy · esc close"
	if h := d.height(); len(lines) > h {
		d.offset = min(max(d.offset, 0), len(lines)-h)
		lines = lines[d.offset : d.offset+h]
		hint = "↑↓ scroll · " + hint
	}
	return frame(t, width, d.title, strings.Join(lines, "\n"), hint)
}

type formDialog struct {
	title  string
	labels []string
	inputs []textinput.Model
	focus  int
	submit func([]string) tea.Cmd
}

func newForm(t theme, title string, params []plugin.Param, submit func([]string) tea.Cmd) *formDialog {
	d := &formDialog{title: title, submit: submit}
	for _, p := range params {
		in := newInput(t, p.Name)
		in.SetValue(p.Value)
		d.labels = append(d.labels, p.Name)
		d.inputs = append(d.inputs, in)
	}
	d.inputs[0].Focus()
	return d
}

func (d *formDialog) setFocus(i int) tea.Cmd {
	d.inputs[d.focus].Blur()
	d.focus = (i + len(d.inputs)) % len(d.inputs)
	return d.inputs[d.focus].Focus()
}

func (d *formDialog) update(msg tea.Msg) (dialog, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			return nil, nil
		case "tab", "down":
			return d, d.setFocus(d.focus + 1)
		case "shift+tab", "up":
			return d, d.setFocus(d.focus - 1)
		case "enter":
			if d.focus < len(d.inputs)-1 {
				return d, d.setFocus(d.focus + 1)
			}
			values := make([]string, len(d.inputs))
			for i, in := range d.inputs {
				values[i] = in.Value()
			}
			return nil, d.submit(values)
		}
	}
	var cmd tea.Cmd
	d.inputs[d.focus], cmd = d.inputs[d.focus].Update(msg)
	return d, cmd
}

func (d *formDialog) view(t theme, width int) string {
	var rows []string
	for i, in := range d.inputs {
		in.SetWidth(max(width-8, 1))
		label := fg(t.muted).Render(d.labels[i])
		if i == d.focus {
			label = fg(t.primary).Bold(true).Render(d.labels[i])
		}
		rows = append(rows, label, in.View(), "")
	}
	return frame(t, width, d.title, strings.Join(rows[:len(rows)-1], "\n"),
		"tab next field · enter run · esc close")
}

type connectDialog struct {
	title  string
	kind   int
	focus  int
	err    string
	name   textinput.Model
	uri    textinput.Model
	check  func(name string) error
	onSave func(profile) tea.Cmd
}

func newConnect(t theme, title string, p profile, check func(string) error, onSave func(profile) tea.Cmd) *connectDialog {
	d := &connectDialog{title: title, check: check, onSave: onSave}
	for i, k := range kinds {
		if k.Name == p.Kind {
			d.kind = i
		}
	}
	d.name = newInput(t, "e.g. local-pg, prod-mongo")
	d.uri = newInput(t, kinds[d.kind].URI)
	d.name.SetValue(p.Name)
	d.uri.SetValue(p.URI)
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
	return frame(t, width, d.title, body, "tab next field · enter save · esc close")
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
