package plugin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const pluginsDir = "../../plugins"

func newTestHost(t *testing.T) *Host {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	if _, err := os.Stat(pluginsDir + "/node_modules"); err != nil {
		t.Skip("plugin dependencies missing, run npm ci in plugins")
	}
	h, err := NewHost(pluginsDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func TestDiscover(t *testing.T) {
	kinds, err := Discover(pluginsDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range kinds {
		names = append(names, k.Name)
	}
	if got := strings.Join(names, ","); got != "kafka,loki,mongodb,postgres,rabbitmq,redis" {
		t.Fatalf("kinds %s", got)
	}
	if _, err := Discover(t.TempDir()); err == nil {
		t.Fatal("empty dir should fail")
	}
}

func TestHostLifecycle(t *testing.T) {
	h := newTestHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := h.Connect(ctx, "redis", "redis://127.0.0.1:1/0"); err == nil ||
		!strings.Contains(err.Error(), "ECONNREFUSED") {
		t.Fatalf("connect to a closed port: %v", err)
	}
	h.mu.Lock()
	p := h.procs["redis"]
	h.mu.Unlock()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-h.Exits():
		if e.Kind != "redis" {
			t.Fatalf("exit for %s", e.Kind)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no exit event after the plugin was killed")
	}
	if _, err := h.Connect(ctx, "redis", "redis://127.0.0.1:1/0"); err == nil ||
		!strings.Contains(err.Error(), "ECONNREFUSED") {
		t.Fatalf("plugin should respawn on next use: %v", err)
	}
	if _, err := h.Connect(ctx, "nope", ""); err == nil {
		t.Fatal("unknown kind should fail")
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func render(r Result) string {
	var b strings.Builder
	b.WriteString(r.Text)
	for _, row := range r.Rows {
		b.WriteString("\n" + strings.Join(row, " | "))
	}
	return b.String()
}

func mustQuery(t *testing.T, s Session, q string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := s.Query(ctx, q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if res.Stream != nil {
		t.Fatalf("%s: unexpected stream", q)
	}
	return render(res)
}

func expectLine(t *testing.T, s Session, q string, trigger func(), want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.Query(ctx, q)
	if err != nil || res.Stream == nil {
		t.Fatalf("%s: stream=%v err=%v", q, res.Stream != nil, err)
	}
	trigger()
	for line := range res.Stream {
		if strings.Contains(line, want) {
			return
		}
	}
	t.Fatalf("%s: stream ended without %q: %v", q, want, res.Err())
}

func pushLoki(t *testing.T, uri, app, line string) {
	t.Helper()
	body := fmt.Sprintf(`{"streams":[{"stream":{"app":%q},"values":[["%d",%q]]}]}`, app, time.Now().UnixNano(), line)
	res, err := http.Post(uri+"/loki/api/v1/push", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode/100 != 2 {
		t.Fatalf("loki push: %s", res.Status)
	}
}

func TestBackends(t *testing.T) {
	if os.Getenv("QUERYPRO_IT") == "" {
		t.Skip("set QUERYPRO_IT=1 with the docker compose services running")
	}
	h := newTestHost(t)
	id := fmt.Sprint(time.Now().UnixNano())
	loki := env("QUERYPRO_IT_LOKI", "http://localhost:3100")
	topic := "qp_it_" + id

	for _, c := range []struct {
		kind, uri, resource string
		setup, check        []string
		live, want          string
		trigger             func(s Session)
		cleanup             []string
	}{
		{
			kind: "postgres", uri: env("QUERYPRO_IT_POSTGRES", "postgres://postgres:postgres@localhost:5432/postgres"),
			resource: "qp_it",
			setup: []string{"DROP TABLE IF EXISTS qp_it; CREATE TABLE qp_it (id int primary key, name text); " +
				"INSERT INTO qp_it VALUES (1, 'ada'), (2, NULL);"},
			check:   []string{"SELECT name FROM qp_it ORDER BY id", "\\d qp_it", "\\dt"},
			cleanup: []string{"DROP TABLE qp_it"},
		},
		{
			kind: "mongodb", uri: env("QUERYPRO_IT_MONGODB", "mongodb://localhost:27017/qp_it"),
			resource: "items",
			setup:    []string{`db.items.insertMany([{n: 1, at: new Date()}, {n: 2}])`},
			check:    []string{`db.items.find({n: {$gt: 1}})`, "show collections", `db.items.aggregate([{$count: "n"}])`},
			cleanup:  []string{"db.dropDatabase()"},
		},
		{
			kind: "redis", uri: env("QUERYPRO_IT_REDIS", "redis://localhost:6379/15"),
			resource: "qp:it:" + id,
			setup:    []string{"SET qp:it:" + id + ` "hello world"`},
			check:    []string{"GET qp:it:" + id, "DBSIZE"},
			live:     "SUBSCRIBE qp-" + id, want: "hi there",
			trigger: func(s Session) { mustQuery(t, s, "PUBLISH qp-"+id+` "hi there"`) },
			cleanup: []string{"DEL qp:it:" + id},
		},
		{
			kind: "rabbitmq", uri: env("QUERYPRO_IT_RABBITMQ", "amqp://guest:guest@localhost:5672/"),
			resource: "qp_it_" + id,
			setup:    []string{"declare qp_it_" + id, `publish "" qp_it_` + id + " hello"},
			check:    []string{"queues", "exchanges", "bindings amq.topic"},
			live:     "tap amq.topic qp." + id + ".#", want: "tapped",
			trigger: func(s Session) { mustQuery(t, s, "publish amq.topic qp."+id+".x tapped") },
			cleanup: []string{"purge qp_it_" + id},
		},
		{
			kind: "kafka", uri: env("QUERYPRO_IT_KAFKA", "kafka://localhost:9092"),
			resource: topic,
			setup:    []string{"create-topic " + topic + " 2", "produce " + topic + " first message"},
			check:    []string{"topics", "groups"},
			live:     "tail " + topic + " from-beginning", want: "first message",
			trigger: func(Session) {},
			cleanup: []string{"delete-topic " + topic},
		},
		{
			kind: "loki", uri: loki,
			resource: `"qp_it_` + id + `"}`,
			check:    []string{"labels"},
			live:     `tail {app="qp_it_` + id + `"}`, want: "second line",
			trigger: func(Session) { pushLoki(t, loki, "qp_it_"+id, "second line") },
		},
	} {
		t.Run(c.kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if c.kind == "loki" {
				pushLoki(t, loki, "qp_it_"+id, "first line error")
			}
			s, err := h.Connect(ctx, c.kind, c.uri)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if s.Server() == "" {
				t.Error("empty server version")
			}
			for _, q := range c.setup {
				mustQuery(t, s, q)
			}
			var found *Resource
			deadline := time.Now().Add(20 * time.Second)
			for found == nil && time.Now().Before(deadline) {
				rs, err := s.Resources(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rs {
					if strings.HasSuffix(r.Name, c.resource) {
						found = &r
					}
				}
				if found == nil {
					time.Sleep(time.Second)
				}
			}
			if found == nil {
				t.Fatalf("resource %s not listed", c.resource)
			}
			acts, err := s.Actions(ctx, *found)
			if err != nil || len(acts) == 0 {
				t.Fatalf("actions: %v %v", acts, err)
			}
			for _, a := range acts {
				if a.Danger || strings.Contains(a.Name, "live") || strings.Contains(a.Name, "tail") {
					continue
				}
				t.Logf("%s → %.80q", a.Query, mustQuery(t, s, a.Query))
			}
			for _, q := range c.check {
				t.Logf("%s → %.80q", q, mustQuery(t, s, q))
			}
			if c.live != "" {
				expectLine(t, s, c.live, func() { c.trigger(s) }, c.want)
			}
			for _, q := range c.cleanup {
				mustQuery(t, s, q)
			}
		})
	}
}

func TestPostgresCancel(t *testing.T) {
	if os.Getenv("QUERYPRO_IT") == "" {
		t.Skip("set QUERYPRO_IT=1 with the docker compose services running")
	}
	h := newTestHost(t)
	ctx := context.Background()
	s, err := h.Connect(ctx, "postgres", env("QUERYPRO_IT_POSTGRES", "postgres://postgres:postgres@localhost:5432/postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	qctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Query(qctx, "SELECT pg_sleep(30)"); err == nil {
		t.Fatal("cancelled query should fail")
	}
	if got := mustQuery(t, s, "SELECT 42"); !strings.Contains(got, "42") || time.Since(start) > 10*time.Second {
		t.Fatalf("session stuck after cancel: %q after %s", got, time.Since(start))
	}
}
