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
}

type Plugin interface {
	Placeholder() string
	Resources() []Resource
	Actions(r Resource) []Action
	Query(ctx context.Context, q string) (Result, error)
}
