package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"querypro/internal/plugin"
	"querypro/internal/store"
)

type fakeHost struct{ connects atomic.Int32 }

func (h *fakeHost) Connect(context.Context, string, string) (plugin.Session, error) {
	h.connects.Add(1)
	return &fakeSession{}, nil
}

var flaky atomic.Int32

type fakeSession struct{}

func (s *fakeSession) Server() string { return "fake" }
func (s *fakeSession) Close() error   { return nil }

func (s *fakeSession) Resources(context.Context) ([]plugin.Resource, error) {
	return []plugin.Resource{{Kind: "table", Name: "users"}}, nil
}

func (s *fakeSession) Actions(context.Context, plugin.Resource) ([]plugin.Action, error) {
	return []plugin.Action{{Name: "Preview", Query: "SELECT 1;"}}, nil
}

func (s *fakeSession) Build(context.Context, plugin.Resource, string, []string) (string, error) {
	return "", nil
}

func (s *fakeSession) Query(ctx context.Context, q string) (plugin.Result, error) {
	if q == "flaky" && flaky.Add(1) == 1 {
		return plugin.Result{}, fmt.Errorf("%w: gone", plugin.ErrDisconnected)
	}
	if q == "tail" {
		ch := make(chan string)
		go func() {
			defer close(ch)
			for i := 0; ; i++ {
				select {
				case ch <- fmt.Sprint("line ", i):
				case <-ctx.Done():
					return
				}
			}
		}()
		return plugin.Result{Stream: ch, Summary: "live", Err: func() error { return nil }}, nil
	}
	return plugin.Result{Columns: []string{"id"}, Rows: [][]string{{"1"}}, Summary: "1 row"}, nil
}

func TestServer(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ps := []store.Profile{{Name: "pg", Kind: "postgres", URI: "postgres://u:secret@h/db"}}
	if err := st.Save(store.ProfilesFile, ps); err != nil {
		t.Fatal(err)
	}
	host := &fakeHost{}
	s := &server{host: host, st: st, sess: map[string]plugin.Session{}}
	ctx := context.Background()
	st1, ct := sdk.NewInMemoryTransports()
	if _, err := s.server("test").Connect(ctx, st1, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) (*sdk.CallToolResult, string) {
		t.Helper()
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res.StructuredContent)
		return res, string(b)
	}

	if _, out := call("connections", nil); strings.Contains(out, "secret") || !strings.Contains(out, `"pg"`) {
		t.Fatalf("connections: %s", out)
	}
	if _, out := call("resources", map[string]any{"connection": "pg"}); !strings.Contains(out, "users") {
		t.Fatalf("resources: %s", out)
	}
	if _, out := call("query", map[string]any{"connection": "pg", "query": "SELECT"}); !strings.Contains(out, `"rows":[["1"]]`) {
		t.Fatalf("query: %s", out)
	}
	if res, out := call("query", map[string]any{"connection": "pg", "query": "flaky"}); res.IsError || host.connects.Load() != 2 {
		t.Fatalf("flaky should reconnect once: %s", out)
	}
	if _, out := call("query", map[string]any{"connection": "pg", "query": "tail", "seconds": 1}); !strings.Contains(out, fmt.Sprintf("line %d", maxLines-1)) {
		t.Fatalf("tail: %.200s", out)
	}
	if res, _ := call("resources", map[string]any{"connection": "nope"}); !res.IsError {
		t.Fatal("unknown connection should be a tool error")
	}
}
