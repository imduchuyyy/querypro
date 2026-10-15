package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"querypro/internal/plugin"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := rootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestConn(t *testing.T) {
	t.Setenv("QUERYPRO_HOME", t.TempDir())
	plugins := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plugins, "pg"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"kind":"postgres","code":"PG","command":["node"]}`
	if err := os.WriteFile(filepath.Join(plugins, "pg", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	p := "--plugins=" + plugins
	if _, err := run(t, p, "conn", "add", "db", "postgres", "postgres://u:secret@h/db"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{
		{"conn", "add", "DB", "postgres", "x"},
		{"conn", "add", "other", "nope", "x"},
		{"conn", "add", "a b", "postgres", "x"},
		{"conn", "rm", "missing"},
	} {
		if _, err := run(t, append([]string{p}, bad...)...); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
	out, err := run(t, "conn", "ls")
	if err != nil || out != "db  postgres\n" {
		t.Fatalf("list = %q, %v", out, err)
	}
	if _, err := run(t, "conn", "rm", "db"); err != nil {
		t.Fatal(err)
	}
	if out, _ := run(t, "conn", "ls"); out != "" {
		t.Fatalf("after rm: %q", out)
	}
}

type fakeSession struct {
	plugin.Session
	res plugin.Result
}

func (s fakeSession) Query(context.Context, string) (plugin.Result, error) { return s.res, nil }

func TestRunQuery(t *testing.T) {
	lines := make(chan string, 2)
	lines <- "a"
	lines <- "b"
	close(lines)
	for _, tc := range []struct {
		res  plugin.Result
		want string
	}{
		{plugin.Result{Columns: []string{"id", "name"}, Rows: [][]string{{"1", "x\ty\nz"}}}, "id  name\n1   x y↵z\n"},
		{plugin.Result{Text: "OK"}, "OK\n"},
		{plugin.Result{Stream: lines, Err: func() error { return nil }}, "a\nb\n"},
	} {
		var out, info strings.Builder
		if err := runQuery(t.Context(), &out, &info, fakeSession{res: tc.res}, "q"); err != nil || out.String() != tc.want {
			t.Errorf("got %q, %v, want %q", out.String(), err, tc.want)
		}
	}
}
