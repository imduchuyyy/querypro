package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"querypro/internal/plugin"
	"querypro/internal/store"
)

const (
	defaultSeconds = 5
	maxSeconds     = 60
	maxLines       = 500
)

type connector interface {
	Connect(ctx context.Context, kind, uri string) (plugin.Session, error)
}

type server struct {
	host connector
	st   *store.Store
	mu   sync.Mutex
	sess map[string]plugin.Session
}

type connection struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type resource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type action struct {
	Name   string `json:"name"`
	Query  string `json:"query"`
	Danger bool   `json:"danger,omitempty"`
}

type connectionsOut struct {
	Connections []connection `json:"connections"`
}

type connIn struct {
	Connection string `json:"connection" jsonschema:"saved connection name"`
}

type resourcesOut struct {
	Resources []resource `json:"resources"`
}

type actionsIn struct {
	Connection string `json:"connection" jsonschema:"saved connection name"`
	Kind       string `json:"kind" jsonschema:"resource kind from the resources tool"`
	Name       string `json:"name" jsonschema:"resource name from the resources tool"`
}

type actionsOut struct {
	Actions []action `json:"actions"`
}

type queryIn struct {
	Connection string `json:"connection" jsonschema:"saved connection name"`
	Query      string `json:"query" jsonschema:"query in the backend's own language"`
	Seconds    int    `json:"seconds,omitempty" jsonschema:"how long to collect a live stream, default 5, max 60"`
}

type queryOut struct {
	Summary string     `json:"summary,omitempty"`
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	Text    string     `json:"text,omitempty"`
	Lines   []string   `json:"lines,omitempty"`
}

func Serve(ctx context.Context, host connector, st *store.Store, version string) error {
	s := &server{host: host, st: st, sess: map[string]plugin.Session{}}
	defer s.close()
	return s.server(version).Run(ctx, &sdk.StdioTransport{})
}

func (s *server) server(version string) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "querypro", Version: version}, nil)
	readOnly := &sdk.ToolAnnotations{ReadOnlyHint: true}
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "connections",
		Description: "List saved querypro connections (name and backend kind).",
		Annotations: readOnly,
	}, s.connections)
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "resources",
		Description: "List the resources (tables, collections, keys, topics, queues…) of a connection.",
		Annotations: readOnly,
	}, s.resources)
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "actions",
		Description: "List example queries for a resource; shows the backend's query language.",
		Annotations: readOnly,
	}, s.actions)
	destructive := true
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "query",
		Description: "Run a query on a connection. Live streams (tail, consume…) are collected for a few seconds.",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: &destructive},
	}, s.query)
	return srv
}

func (s *server) profiles() ([]store.Profile, error) {
	var ps []store.Profile
	return ps, s.st.Load(store.ProfilesFile, &ps)
}

func (s *server) connections(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, connectionsOut, error) {
	ps, err := s.profiles()
	out := connectionsOut{Connections: []connection{}}
	for _, p := range ps {
		out.Connections = append(out.Connections, connection{Name: p.Name, Kind: p.Kind})
	}
	return nil, out, err
}

func (s *server) resources(ctx context.Context, _ *sdk.CallToolRequest, in connIn) (*sdk.CallToolResult, resourcesOut, error) {
	out := resourcesOut{Resources: []resource{}}
	err := s.with(ctx, in.Connection, func(sess plugin.Session) error {
		rs, err := sess.Resources(ctx)
		out.Resources = out.Resources[:0]
		for _, r := range rs {
			out.Resources = append(out.Resources, resource(r))
		}
		return err
	})
	return nil, out, err
}

func (s *server) actions(ctx context.Context, _ *sdk.CallToolRequest, in actionsIn) (*sdk.CallToolResult, actionsOut, error) {
	out := actionsOut{Actions: []action{}}
	err := s.with(ctx, in.Connection, func(sess plugin.Session) error {
		as, err := sess.Actions(ctx, plugin.Resource{Kind: in.Kind, Name: in.Name})
		out.Actions = out.Actions[:0]
		for _, a := range as {
			out.Actions = append(out.Actions, action{Name: a.Name, Query: a.Query, Danger: a.Danger})
		}
		return err
	})
	return nil, out, err
}

func (s *server) query(ctx context.Context, _ *sdk.CallToolRequest, in queryIn) (*sdk.CallToolResult, queryOut, error) {
	secs := in.Seconds
	if secs <= 0 {
		secs = defaultSeconds
	}
	secs = min(secs, maxSeconds)
	var out queryOut
	err := s.with(ctx, in.Connection, func(sess plugin.Session) error {
		qctx, cancel := context.WithTimeout(ctx, time.Duration(secs)*time.Second)
		defer cancel()
		res, err := sess.Query(qctx, in.Query)
		if err != nil {
			return err
		}
		out = queryOut{Summary: res.Summary, Columns: res.Columns, Rows: res.Rows, Text: res.Text}
		if res.Stream == nil {
			return nil
		}
		for line := range res.Stream {
			out.Lines = append(out.Lines, line)
			if len(out.Lines) == maxLines {
				break
			}
		}
		cancel()
		for range res.Stream {
		}
		return res.Err()
	})
	return nil, out, err
}

func (s *server) with(ctx context.Context, name string, fn func(plugin.Session) error) error {
	sess, err := s.session(ctx, name)
	if err != nil {
		return err
	}
	if err = fn(sess); !errors.Is(err, plugin.ErrDisconnected) {
		return err
	}
	s.drop(name, sess)
	if sess, err = s.session(ctx, name); err != nil {
		return err
	}
	return fn(sess)
}

func (s *server) session(ctx context.Context, name string) (plugin.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sess[name]; ok {
		return sess, nil
	}
	ps, err := s.profiles()
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.Name == name {
			sess, err := s.host.Connect(ctx, p.Kind, p.URI)
			if err != nil {
				return nil, fmt.Errorf("connect %s: %w", name, err)
			}
			s.sess[name] = sess
			return sess, nil
		}
	}
	return nil, fmt.Errorf("no saved connection named %q", name)
}

func (s *server) drop(name string, sess plugin.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess[name] == sess {
		delete(s.sess, name)
		_ = sess.Close()
	}
}

func (s *server) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sess {
		_ = sess.Close()
	}
}
