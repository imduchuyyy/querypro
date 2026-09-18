package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type theme struct {
	name    string
	bg      color.Color
	border  color.Color
	text    color.Color
	muted   color.Color
	primary color.Color
	accent  color.Color
	success color.Color
	err     color.Color
}

var hex = lipgloss.Color

var themes = []theme{
	{
		name: "querypro", bg: hex("#0b0f14"), border: hex("#2a3544"),
		text: hex("#d6deeb"), muted: hex("#6b7a90"), primary: hex("#3dd6b5"),
		accent: hex("#f6b85c"), success: hex("#7ee787"), err: hex("#ff6b6b"),
	},
	{
		name: "tokyonight", bg: hex("#1a1b26"), border: hex("#3b4261"),
		text: hex("#c8d3f5"), muted: hex("#828bb8"), primary: hex("#82aaff"),
		accent: hex("#c099ff"), success: hex("#c3e88d"), err: hex("#ff757f"),
	},
	{
		name: "catppuccin", bg: hex("#1e1e2e"), border: hex("#45475a"),
		text: hex("#cdd6f4"), muted: hex("#a6adc8"), primary: hex("#89b4fa"),
		accent: hex("#cba6f7"), success: hex("#a6e3a1"), err: hex("#f38ba8"),
	},
}

func fg(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c)
}

func pill(label string, bg, text color.Color) string {
	return lipgloss.NewStyle().
		Background(bg).Foreground(text).Bold(true).
		Padding(0, 1).Render(label)
}

type kind struct {
	name  string
	code  string
	color color.Color
	uri   string
}

var kinds = []kind{
	{"postgres", "PG", hex("#5b9bd5"), "postgres://postgres:postgres@localhost:5432/postgres"},
	{"mongodb", "MG", hex("#4db33d"), "mongodb://localhost:27017"},
	{"redis", "RD", hex("#e5534b"), "redis://localhost:6379"},
	{"rabbitmq", "MQ", hex("#ff8a3d"), "amqp://guest:guest@localhost:5672/"},
	{"kafka", "KF", hex("#b4bccb"), "localhost:9092"},
	{"loki", "LK", hex("#f2cc0c"), "http://localhost:3100"},
}

func kindOf(name string) kind {
	for _, k := range kinds {
		if k.name == name {
			return k
		}
	}
	return kind{name: name, code: "??", color: hex("#808080")}
}

func badge(t theme, name string, filled bool) string {
	k := kindOf(name)
	if filled {
		return pill(k.code, k.color, t.bg)
	}
	return fg(k.color).Bold(true).Padding(0, 1).Render(k.code)
}
