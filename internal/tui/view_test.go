package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"querypro/internal/plugin"
)

var wideRow = []string{"7", "ada@example.com", strings.Repeat("payload ", 40) + "\nsecond line"}

func TestFitColumns(t *testing.T) {
	m := newModel(fakeHost{}, nil)
	cols := []string{"id", "email", "note"}
	rows := [][]string{wideRow, {"8", "grace@example.com", "short"}}
	for _, w := range []int{120, 60, 24} {
		out, _, _ := m.tableView(cols, rows, w, 0)
		if got := lipgloss.Width(out); got > w {
			t.Errorf("width %d: table is %d wide\n%s", w, got, out)
		}
		if got := lipgloss.Height(out); got != len(rows)+4 {
			t.Errorf("width %d: %d lines, want one per row\n%s", w, got, out)
		}
		one := [][]string{{wideRow[2]}}
		if single, _, shown := m.tableView(cols[2:], one, w, 0); shown != 1 || !strings.Contains(single, "…") {
			t.Errorf("width %d: a single wide column must be truncated\n%s", w, single)
		}
	}
	if _, flat, _ := fitColumns(cols, rows, 200, 0); strings.Contains(flat[0][2], "\n") {
		t.Error("newlines must not survive in a cell")
	}
}

func TestRowDetail(t *testing.T) {
	writeClipboard = func(string) error { return nil }
	m := seed(t)
	e := &entry{id: 1, res: plugin.Result{
		Columns: []string{"id", "email", "note"},
		Rows:    [][]string{{"1", "a@b.c", "short"}, wideRow},
	}}
	m.conn().entries = []*entry{e}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.View()

	if len(m.rows) != 1 || m.rows[0].to-m.rows[0].from != 2 {
		t.Fatalf("row spans %+v", m.rows)
	}
	ox, oy := m.origin()
	y := oy + m.rows[0].from + 1 - m.vp.YOffset()
	m.Update(tea.MouseClickMsg{X: ox + 4, Y: y, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: ox + 4, Y: y, Button: tea.MouseLeft})
	do(m, cmd)

	d, ok := m.dlg.(*detailDialog)
	if !ok {
		t.Fatalf("clicking a row opened %#v", m.dlg)
	}
	if d.title != "Row 2 of 2" || !strings.Contains(d.text(), wideRow[2]) {
		t.Fatalf("detail %q: %.60q", d.title, d.text())
	}
	view := d.view(m.t(), 72)
	if lipgloss.Width(view) > 72 || !strings.Contains(view, "second line") {
		t.Fatalf("detail view %d wide:\n%s", lipgloss.Width(view), view)
	}
	m.height = 16
	if view := d.view(m.t(), 72); !strings.Contains(view, "scroll") {
		t.Fatalf("a detail taller than the screen must scroll:\n%s", view)
	}
	if _, cmd := d.update(tea.KeyPressMsg{Code: 'c', Text: "c"}); cmd == nil {
		t.Error("c should copy the row")
	}
	if next, _ := d.update(tea.KeyPressMsg{Code: tea.KeyEscape}); next != nil {
		t.Error("esc should close the detail")
	}
}

func TestColumnWindow(t *testing.T) {
	m := newModel(fakeHost{}, nil)
	cols := make([]string, 12)
	row := make([]string, 12)
	for i := range cols {
		cols[i] = fmt.Sprintf("column_%02d", i)
		row[i] = strings.Repeat("v", 30)
	}
	rows := [][]string{row}
	out, from, shown := m.tableView(cols, rows, 100, 0)
	if from != 0 || shown < 2 || shown >= len(cols) {
		t.Fatalf("window from=%d shown=%d\n%s", from, shown, out)
	}
	if !strings.Contains(out, cols[shown-1]) || strings.Contains(out, cols[shown]) {
		t.Fatalf("shown columns do not match %d\n%s", shown, out)
	}
	if _, from, _ := m.tableView(cols, rows, 100, 5); from != 5 {
		t.Fatalf("offset ignored, from=%d", from)
	}
	if out, _, shown := m.tableView(cols, rows, 100, len(cols)-1); shown != 1 || !strings.Contains(out, cols[11]) {
		t.Fatalf("last column alone: shown=%d\n%s", shown, out)
	}

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	do(m, m.saveProfile(profile{Name: "a", Kind: "postgres", URI: "x"}))
	e := &entry{res: plugin.Result{Columns: cols, Rows: rows}}
	m.conn().entries = []*entry{e}
	right := tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}
	left := tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}
	m.Update(right)
	m.Update(right)
	if e.colOffset != 2 {
		t.Fatalf("shift+right twice: offset=%d", e.colOffset)
	}
	for range 20 {
		m.Update(right)
	}
	if e.colOffset != len(cols)-1 {
		t.Fatalf("offset must stop at the last column, got %d", e.colOffset)
	}
	for range 20 {
		m.Update(left)
	}
	if e.colOffset != 0 {
		t.Fatalf("offset must stop at the first column, got %d", e.colOffset)
	}
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelRight})
	if e.colOffset != 1 {
		t.Fatalf("horizontal wheel: offset=%d", e.colOffset)
	}
	if !strings.Contains(m.View().Content, "columns 2-") {
		t.Fatal("the status line should say which columns are visible")
	}
}

func TestSidebarScroll(t *testing.T) {
	m := seed(t)
	c := m.conn()
	c.resources = nil
	for i := range 100 {
		c.resources = append(c.resources, plugin.Resource{Name: fmt.Sprintf("res%03d", i), Kind: "table"})
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if got := lipgloss.Height(m.sidebarView()); got != 30-tablineHeight {
		t.Fatalf("sidebar is %d lines high", got)
	}
	m.View()
	first := m.clicks[0].y
	for range 50 {
		m.Update(tea.MouseWheelMsg{X: 1, Y: first, Button: tea.MouseWheelDown})
	}
	out := m.View().Content
	if !strings.Contains(out, "res099") || strings.Contains(out, "res000") {
		t.Fatalf("scrolling should reach the last resource\n%s", out)
	}
	m.clicks[0].fn()
	if c.current == nil || c.current.Name != fmt.Sprintf("res%03d", c.resOffset) {
		t.Fatalf("click opened %+v at offset %d", c.current, c.resOffset)
	}
	end := c.resOffset
	m.Update(tea.MouseWheelMsg{X: 1, Y: first, Button: tea.MouseWheelUp})
	if c.resOffset != end-3 {
		t.Fatalf("offset %d after scrolling up", c.resOffset)
	}
}

func TestPaletteGroups(t *testing.T) {
	m := seed(t)
	d := m.palette().(*listDialog)
	out := d.view(m.t(), 72)
	for _, g := range []string{"CONNECTION", "RESOURCES", "QUERY", "TABS", "PREFERENCES", "APP"} {
		if strings.Count(out, g) != 1 {
			t.Errorf("group %s should show once\n%s", g, out)
		}
	}
	d.search.SetValue("connection")
	for _, it := range d.visible() {
		if it.group != "Connection" && !strings.Contains(strings.ToLower(it.label), "connection") {
			t.Errorf("search matched %q in %s", it.label, it.group)
		}
	}
}

func TestFuzzySearch(t *testing.T) {
	m := seed(t)
	d := m.palette().(*listDialog)
	for q, want := range map[string]string{
		"rcn":    "Reconnect",
		"edcon":  "Edit saved connection",
		"quit":   "Quit",
		"tgsdbr": "Toggle sidebar",
	} {
		d.search.SetValue(q)
		if got := d.visible(); len(got) == 0 || got[0].label != want {
			t.Errorf("%q: got %v, want %s first", q, got, want)
		}
	}
	d.search.SetValue("zzz")
	if got := d.visible(); len(got) != 0 {
		t.Errorf("no match should list nothing, got %d", len(got))
	}
	if _, ok := fuzzy("users", "u_order_users"); !ok {
		t.Error("subsequence should match")
	}
	a, _ := fuzzy("users", "users")
	b, _ := fuzzy("users", "u_order_users")
	if a <= b {
		t.Errorf("exact %d should beat scattered %d", a, b)
	}
}

func TestLongList(t *testing.T) {
	m := seed(t)
	c := m.conn()
	c.resources = nil
	for i := range 1000 {
		c.resources = append(c.resources, plugin.Resource{Name: fmt.Sprintf("key:%04d", i), Kind: "hash"})
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.dlg = m.resources()
	if got := lipgloss.Height(m.View().Content); got != 30 {
		t.Fatalf("a long list must fit the screen, got %d lines", got)
	}
	for range 120 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if out := m.View().Content; !strings.Contains(out, "key:0999") || !strings.Contains(out, "1000/1000") {
		t.Fatalf("the cursor should stay visible at the end\n%s", out)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 80})
	m.View()
	if got := strings.Count(m.dlg.view(m.t(), 72), "key:"); got != maxListRows {
		t.Fatalf("a tall screen should still show %d rows, got %d", maxListRows, got)
	}
	m.dlg = m.palette()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	for range 30 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if out := m.View().Content; lipgloss.Height(out) != 20 || !strings.Contains(out, "Quit") {
		t.Fatalf("grouped list should scroll within the screen\n%s", out)
	}
}
