package plugin

import "context"

type Resource struct {
	Kind string
	Name string
}

type Action struct {
	Name   string
	Query  string
	Danger bool
}

type Result struct {
	Columns []string
	Rows    [][]string
	Text    string
	Summary string
	Stream  <-chan string
	Err     func() error
}

type Session interface {
	Server() string
	Resources(ctx context.Context) ([]Resource, error)
	Actions(ctx context.Context, r Resource) ([]Action, error)
	Query(ctx context.Context, q string) (Result, error)
	Close() error
}

type Kind struct {
	Name        string   `json:"kind"`
	Code        string   `json:"code"`
	Color       string   `json:"color"`
	URI         string   `json:"uri"`
	Placeholder string   `json:"placeholder"`
	Command     []string `json:"command"`
	Dir         string   `json:"-"`
}
