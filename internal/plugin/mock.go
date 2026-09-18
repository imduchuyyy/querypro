package plugin

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type mock struct {
	placeholder string
	resources   []Resource
	actions     func(Resource) []Action
	query       func(ctx context.Context, q string) (Result, error)
}

func (m *mock) Placeholder() string         { return m.placeholder }
func (m *mock) Resources() []Resource       { return m.resources }
func (m *mock) Actions(r Resource) []Action { return m.actions(r) }

var latency = func() time.Duration {
	return time.Duration(80+rand.IntN(320)) * time.Millisecond
}

func (m *mock) Query(ctx context.Context, q string) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-time.After(latency()):
	}
	return m.query(ctx, strings.TrimSpace(q))
}

var mocks = map[string]func() *mock{
	"postgres": mockPostgres,
	"mongodb":  mockMongo,
	"redis":    mockRedis,
	"rabbitmq": mockRabbit,
	"kafka":    mockKafka,
	"loki":     mockLoki,
}

func Mock(kind string) Plugin {
	return mocks[kind]()
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func table(cols []string, rows [][]string) Result {
	return Result{Columns: cols, Rows: rows, Summary: plural(len(rows), "row")}
}

func stream(ctx context.Context, every time.Duration, next func(i int) string) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(every + time.Duration(rand.IntN(400))*time.Millisecond):
			}
			select {
			case ch <- next(i):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

func unknown(q, examples string) error {
	word, _, _ := strings.Cut(q, " ")
	return fmt.Errorf("unknown command %q, try: %s", word, examples)
}

type pgTable struct {
	kind string
	cols [][2]string
	rows [][]string
}

var pgOrder = []string{"users", "orders", "products", "active_users"}

var pgTables = map[string]pgTable{
	"users": {"table", [][2]string{
		{"id", "bigint"}, {"email", "text"}, {"name", "text"}, {"created_at", "timestamptz"},
	}, [][]string{
		{"1", "ada@example.com", "Ada Lovelace", "2026-01-04 09:12"},
		{"2", "linus@example.com", "Linus Torvalds", "2026-01-09 17:40"},
		{"3", "grace@example.com", "Grace Hopper", "2026-02-11 08:03"},
		{"4", "ken@example.com", "Ken Thompson", "2026-03-22 13:55"},
		{"5", "barbara@example.com", "Barbara Liskov", "2026-05-30 11:27"},
		{"6", "dennis@example.com", "Dennis Ritchie", "2026-07-18 20:14"},
	}},
	"orders": {"table", [][2]string{
		{"id", "bigint"}, {"user_id", "bigint"}, {"total", "numeric"}, {"status", "text"},
	}, [][]string{
		{"1041", "1", "42.50", "paid"},
		{"1042", "3", "18.00", "shipped"},
		{"1043", "2", "310.99", "pending"},
		{"1044", "5", "7.25", "refunded"},
		{"1045", "1", "99.00", "paid"},
	}},
	"products": {"table", [][2]string{
		{"id", "bigint"}, {"sku", "text"}, {"name", "text"}, {"price", "numeric"},
	}, [][]string{
		{"1", "KB-01", "Mechanical keyboard", "129.00"},
		{"2", "MS-02", "Trackball mouse", "59.00"},
		{"3", "MN-27", "27in monitor", "349.00"},
	}},
	"active_users": {"view", [][2]string{
		{"id", "bigint"}, {"email", "text"}, {"last_seen", "timestamptz"},
	}, [][]string{
		{"1", "ada@example.com", "2026-09-18 14:02"},
		{"3", "grace@example.com", "2026-09-18 13:47"},
	}},
}

var (
	pgCount    = regexp.MustCompile(`(?is)^select\s+count\(\*\)\s+from\s+(\w+)`)
	pgSelect   = regexp.MustCompile(`(?is)^select\s+.+?\s+from\s+(\w+)(?:.*?\blimit\s+(\d+))?`)
	pgDescribe = regexp.MustCompile(`^\\d\s+(\w+)`)
	pgWrite    = regexp.MustCompile(`(?i)^(insert|update|delete|truncate)\b`)
)

func mockPostgres() *mock {
	m := &mock{placeholder: "SELECT * FROM users LIMIT 10;"}
	for _, name := range pgOrder {
		m.resources = append(m.resources, Resource{Kind: pgTables[name].kind, Name: name})
	}
	m.actions = func(r Resource) []Action {
		acts := []Action{
			{Name: "Preview rows", Query: fmt.Sprintf("SELECT * FROM %s LIMIT 50;", r.Name)},
			{Name: "Count rows", Query: fmt.Sprintf("SELECT count(*) FROM %s;", r.Name)},
			{Name: "Describe", Query: `\d ` + r.Name},
		}
		if r.Kind == "table" {
			acts = append(acts, Action{
				Name: "Truncate", Query: fmt.Sprintf("TRUNCATE %s;", r.Name), Danger: true,
			})
		}
		return acts
	}
	lookup := func(name string) (pgTable, error) {
		t, ok := pgTables[strings.ToLower(name)]
		if !ok {
			return t, fmt.Errorf("relation %q does not exist", name)
		}
		return t, nil
	}
	m.query = func(_ context.Context, q string) (Result, error) {
		if q == `\dt` {
			var rows [][]string
			for _, name := range pgOrder {
				t := pgTables[name]
				rows = append(rows, []string{"public", name, t.kind, strconv.Itoa(len(t.rows))})
			}
			return table([]string{"schema", "name", "type", "rows"}, rows), nil
		}
		if g := pgDescribe.FindStringSubmatch(q); g != nil {
			t, err := lookup(g[1])
			if err != nil {
				return Result{}, err
			}
			var rows [][]string
			for _, c := range t.cols {
				rows = append(rows, []string{c[0], c[1], "not null"})
			}
			return table([]string{"column", "type", "nullable"}, rows), nil
		}
		if g := pgCount.FindStringSubmatch(q); g != nil {
			t, err := lookup(g[1])
			if err != nil {
				return Result{}, err
			}
			return table([]string{"count"}, [][]string{{strconv.Itoa(len(t.rows))}}), nil
		}
		if g := pgSelect.FindStringSubmatch(q); g != nil {
			t, err := lookup(g[1])
			if err != nil {
				return Result{}, err
			}
			rows := t.rows
			if n, err := strconv.Atoi(g[2]); err == nil && n < len(rows) {
				rows = rows[:n]
			}
			var cols []string
			for _, c := range t.cols {
				cols = append(cols, c[0])
			}
			return table(cols, rows), nil
		}
		if g := pgWrite.FindStringSubmatch(q); g != nil {
			tag := map[string]string{
				"insert": "INSERT 0 1", "update": "UPDATE 3",
				"delete": "DELETE 2", "truncate": "TRUNCATE TABLE",
			}[strings.ToLower(g[1])]
			return Result{Summary: tag + " (mock, nothing changed)"}, nil
		}
		return Result{}, unknown(q, `SELECT * FROM users; \dt; \d orders`)
	}
	return m
}

var mongoDocs = map[string][]string{
	"users": {
		`{ _id: ObjectId("66f1a0"), email: "ada@example.com", plan: "pro", tags: ["admin"] }`,
		`{ _id: ObjectId("66f1a1"), email: "grace@example.com", plan: "free", tags: [] }`,
		`{ _id: ObjectId("66f1a2"), email: "ken@example.com", plan: "team", tags: ["beta"] }`,
	},
	"orders": {
		`{ _id: ObjectId("66f2b0"), user: "ada", items: 3, total: 42.5, status: "paid" }`,
		`{ _id: ObjectId("66f2b1"), user: "ken", items: 1, total: 7.25, status: "refunded" }`,
	},
	"events": {
		`{ _id: ObjectId("66f3c0"), type: "login", user: "ada", at: ISODate("2026-09-18T14:02:11Z") }`,
		`{ _id: ObjectId("66f3c1"), type: "checkout", user: "ken", at: ISODate("2026-09-18T14:05:40Z") }`,
		`{ _id: ObjectId("66f3c2"), type: "logout", user: "ada", at: ISODate("2026-09-18T14:31:09Z") }`,
	},
}

var (
	mongoCall  = regexp.MustCompile(`^db\.(\w+)\.(\w+)\(`)
	mongoLimit = regexp.MustCompile(`\.limit\((\d+)\)`)
)

func mockMongo() *mock {
	names := []string{"users", "orders", "events"}
	m := &mock{placeholder: "db.users.find().limit(10)"}
	for _, n := range names {
		m.resources = append(m.resources, Resource{Kind: "collection", Name: n})
	}
	m.actions = func(r Resource) []Action {
		return []Action{
			{Name: "Find documents", Query: fmt.Sprintf("db.%s.find().limit(20)", r.Name)},
			{Name: "Count documents", Query: fmt.Sprintf("db.%s.countDocuments()", r.Name)},
			{Name: "List indexes", Query: fmt.Sprintf("db.%s.getIndexes()", r.Name)},
			{Name: "Drop collection", Query: fmt.Sprintf("db.%s.drop()", r.Name), Danger: true},
		}
	}
	m.query = func(_ context.Context, q string) (Result, error) {
		if q == "show collections" {
			var rows [][]string
			for _, n := range names {
				rows = append(rows, []string{n, strconv.Itoa(len(mongoDocs[n])), "16 KB"})
			}
			return table([]string{"collection", "documents", "size"}, rows), nil
		}
		g := mongoCall.FindStringSubmatch(q)
		if g == nil {
			return Result{}, unknown(q, "show collections · db.users.find() · db.orders.countDocuments()")
		}
		docs, ok := mongoDocs[g[1]]
		if !ok {
			return Result{Text: "[]", Summary: "0 documents"}, nil
		}
		switch g[2] {
		case "find":
			if l := mongoLimit.FindStringSubmatch(q); l != nil {
				if n, _ := strconv.Atoi(l[1]); n < len(docs) {
					docs = docs[:n]
				}
			}
			return Result{Text: strings.Join(docs, "\n"), Summary: plural(len(docs), "document")}, nil
		case "countDocuments":
			return Result{Text: strconv.Itoa(len(docs)), Summary: "1 value"}, nil
		case "getIndexes":
			return table([]string{"name", "key", "unique"}, [][]string{
				{"_id_", "{ _id: 1 }", "true"},
				{"email_1", "{ email: 1 }", "true"},
			}), nil
		case "drop":
			return Result{Text: "true", Summary: "dropped (mock, nothing changed)"}, nil
		}
		return Result{}, fmt.Errorf("db.%s.%s is not a function", g[1], g[2])
	}
	return m
}

type redisKey struct {
	name  string
	typ   string
	ttl   int
	str   string
	pairs [][2]string
	list  []string
}

var redisKeys = []redisKey{
	{name: "session:8f2a", typ: "string", ttl: 1740, str: `{"user":1,"csrf":"a9f"}`},
	{name: "user:42", typ: "hash", ttl: -1, pairs: [][2]string{
		{"name", "Ada"}, {"plan", "pro"}, {"logins", "118"},
	}},
	{name: "queue:emails", typ: "list", ttl: -1, list: []string{
		"welcome:ada@example.com", "reset:ken@example.com", "digest:grace@example.com",
	}},
	{name: "leaderboard", typ: "zset", ttl: -1, pairs: [][2]string{
		{"grace", "9120"}, {"ada", "8870"}, {"linus", "7010"},
	}},
	{name: "rate:api:10.0.3.7", typ: "string", ttl: 42, str: "17"},
}

func mockRedis() *mock {
	m := &mock{placeholder: "KEYS *"}
	for _, k := range redisKeys {
		m.resources = append(m.resources, Resource{Kind: k.typ, Name: k.name})
	}
	read := map[string]string{
		"string": "GET %s", "hash": "HGETALL %s",
		"list": "LRANGE %s 0 -1", "zset": "ZRANGE %s 0 -1 WITHSCORES",
	}
	m.actions = func(r Resource) []Action {
		return []Action{
			{Name: "Read value", Query: fmt.Sprintf(read[r.Kind], r.Name)},
			{Name: "Time to live", Query: "TTL " + r.Name},
			{Name: "Delete key", Query: "DEL " + r.Name, Danger: true},
		}
	}
	find := func(name string) (redisKey, bool) {
		for _, k := range redisKeys {
			if k.name == name {
				return k, true
			}
		}
		return redisKey{}, false
	}
	pairs := func(cols []string, p [][2]string) Result {
		var rows [][]string
		for _, kv := range p {
			rows = append(rows, []string{kv[0], kv[1]})
		}
		return table(cols, rows)
	}
	m.query = func(_ context.Context, q string) (Result, error) {
		f := strings.Fields(q)
		if len(f) == 0 {
			return Result{}, fmt.Errorf("empty command")
		}
		cmd := strings.ToUpper(f[0])
		if cmd == "PING" {
			return Result{Text: "PONG", Summary: "ok"}, nil
		}
		if cmd == "KEYS" {
			pat := "*"
			if len(f) > 1 {
				pat = f[1]
			}
			var rows [][]string
			for _, k := range redisKeys {
				if ok, _ := path.Match(pat, k.name); ok {
					rows = append(rows, []string{k.name, k.typ, strconv.Itoa(k.ttl)})
				}
			}
			return table([]string{"key", "type", "ttl"}, rows), nil
		}
		if len(f) < 2 {
			return Result{}, unknown(q, "KEYS * · GET session:8f2a · HGETALL user:42")
		}
		k, ok := find(f[1])
		switch cmd {
		case "SET":
			return Result{Text: "OK", Summary: "ok (mock, nothing changed)"}, nil
		case "DEL":
			return Result{Text: fmt.Sprintf("(integer) %d", map[bool]int{true: 1}[ok]),
				Summary: "mock, nothing changed"}, nil
		case "TTL":
			if !ok {
				return Result{Text: "(integer) -2", Summary: "key not found"}, nil
			}
			return Result{Text: fmt.Sprintf("(integer) %d", k.ttl), Summary: "seconds"}, nil
		case "TYPE":
			return Result{Text: or(k.typ, "none"), Summary: "type"}, nil
		}
		if !ok {
			return Result{Text: "(nil)", Summary: "key not found"}, nil
		}
		want := map[string]string{"GET": "string", "HGETALL": "hash", "LRANGE": "list", "ZRANGE": "zset"}[cmd]
		if want == "" {
			return Result{}, unknown(q, "KEYS * · GET session:8f2a · HGETALL user:42")
		}
		if want != k.typ {
			return Result{}, fmt.Errorf("WRONGTYPE operation against a key holding the wrong kind of value")
		}
		switch k.typ {
		case "string":
			return Result{Text: k.str, Summary: plural(len(k.str), "byte")}, nil
		case "hash":
			return pairs([]string{"field", "value"}, k.pairs), nil
		case "zset":
			return pairs([]string{"member", "score"}, k.pairs), nil
		}
		var rows [][]string
		for i, v := range k.list {
			rows = append(rows, []string{strconv.Itoa(i), v})
		}
		return table([]string{"index", "value"}, rows), nil
	}
	return m
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func mockRabbit() *mock {
	queues := [][]string{
		{"emails", "12", "1", "2"},
		{"payments", "0", "0", "1"},
		{"emails.dead-letter", "37", "0", "0"},
	}
	exchanges := [][]string{
		{"events", "topic", "3"},
		{"amq.direct", "direct", "0"},
	}
	m := &mock{placeholder: "queues"}
	for _, q := range queues {
		m.resources = append(m.resources, Resource{Kind: "queue", Name: q[0]})
	}
	for _, e := range exchanges {
		m.resources = append(m.resources, Resource{Kind: "exchange", Name: e[0]})
	}
	m.actions = func(r Resource) []Action {
		if r.Kind == "exchange" {
			return []Action{{Name: "Show bindings", Query: "bindings " + r.Name}}
		}
		return []Action{
			{Name: "Peek messages", Query: "peek " + r.Name + " 5"},
			{Name: "Consume (live)", Query: "consume " + r.Name},
			{Name: "Purge queue", Query: "purge " + r.Name, Danger: true},
		}
	}
	const examples = "queues · exchanges · peek emails 5 · consume emails"
	m.query = func(ctx context.Context, q string) (Result, error) {
		f := strings.Fields(q)
		switch {
		case q == "queues":
			return table([]string{"queue", "ready", "unacked", "consumers"}, queues), nil
		case q == "exchanges":
			return table([]string{"exchange", "type", "bindings"}, exchanges), nil
		case len(f) >= 2 && f[0] == "peek":
			n := 5
			if len(f) > 2 {
				n, _ = strconv.Atoi(f[2])
			}
			var rows [][]string
			for i := range n {
				rows = append(rows, []string{strconv.Itoa(i + 1), "false",
					fmt.Sprintf(`{"to":"user%d@example.com","template":"welcome"}`, i+1)})
			}
			return table([]string{"#", "redelivered", "payload"}, rows), nil
		case len(f) == 2 && f[0] == "consume":
			return Result{Summary: "consuming " + f[1], Stream: stream(ctx, 700*time.Millisecond, func(i int) string {
				return fmt.Sprintf("delivery #%d  {\"to\":\"user%d@example.com\",\"template\":\"welcome\"}", i+1, rand.IntN(900))
			})}, nil
		case len(f) == 2 && f[0] == "purge":
			return Result{Summary: "purged 12 messages (mock, nothing changed)"}, nil
		case len(f) == 2 && f[0] == "bindings":
			return table([]string{"source", "destination", "routing key"}, [][]string{
				{f[1], "emails", "user.*"},
				{f[1], "payments", "order.paid"},
			}), nil
		}
		return Result{}, unknown(q, examples)
	}
	return m
}

func mockKafka() *mock {
	topics := [][]string{
		{"orders.created", "6", "3", "184213"},
		{"payments.settled", "3", "3", "90112"},
		{"audit.log", "1", "1", "5120"},
	}
	m := &mock{placeholder: "topics"}
	for _, t := range topics {
		m.resources = append(m.resources, Resource{Kind: "topic", Name: t[0]})
	}
	m.actions = func(r Resource) []Action {
		return []Action{
			{Name: "Consume (live)", Query: "consume " + r.Name},
			{Name: "Describe partitions", Query: "describe " + r.Name},
			{Name: "Delete topic", Query: "delete-topic " + r.Name, Danger: true},
		}
	}
	m.query = func(ctx context.Context, q string) (Result, error) {
		f := strings.Fields(q)
		switch {
		case q == "topics":
			return table([]string{"topic", "partitions", "replicas", "messages"}, topics), nil
		case q == "groups":
			return table([]string{"group", "state", "members", "lag"}, [][]string{
				{"billing", "Stable", "3", "12"},
				{"search-indexer", "Rebalancing", "2", "4031"},
			}), nil
		case len(f) == 2 && f[0] == "describe":
			var rows [][]string
			for p := range 3 {
				rows = append(rows, []string{strconv.Itoa(p), strconv.Itoa(p + 1),
					strconv.Itoa(61000 + p*137), strconv.Itoa(rand.IntN(40))})
			}
			return table([]string{"partition", "leader", "offset", "lag"}, rows), nil
		case len(f) == 2 && f[0] == "consume":
			return Result{Summary: "consuming " + f[1], Stream: stream(ctx, 500*time.Millisecond, func(i int) string {
				return fmt.Sprintf("p%d@%d  key=ord_%d  {\"total\":%.2f,\"status\":\"created\"}",
					rand.IntN(6), 184213+i, 5000+i, rand.Float64()*300)
			})}, nil
		case len(f) == 2 && f[0] == "delete-topic":
			return Result{Summary: "topic deleted (mock, nothing changed)"}, nil
		}
		return Result{}, unknown(q, "topics · groups · describe orders.created · consume orders.created")
	}
	return m
}

var lokiLines = map[string][]string{
	"api": {
		"GET /users 200 12ms", "POST /orders 201 48ms", "GET /orders/991 404 3ms",
		"POST /login 500 210ms error: upstream timeout", "GET /health 200 1ms",
	},
	"worker": {
		"job email.send done in 320ms", "job invoice.render failed: error template missing",
		"job sync.crm done in 1.2s", "job cleanup.sessions done in 88ms",
	},
	"gateway": {
		"route /api -> api:8080", "rate limit hit 10.0.3.7",
		"tls handshake error from 10.0.3.9", "route /ws -> realtime:9000",
	},
}

var lokiQuery = regexp.MustCompile(`^(tail\s+)?\{app="(\w+)"\}(?:\s*\|=\s*"([^"]*)")?$`)

func logLevel(line string) string {
	switch {
	case strings.Contains(line, "error"):
		return "ERROR"
	case strings.Contains(line, "404"), strings.Contains(line, "rate limit"):
		return "WARN"
	}
	return "INFO"
}

func mockLoki() *mock {
	m := &mock{placeholder: `{app="api"} |= "error"`}
	for _, app := range []string{"api", "worker", "gateway"} {
		m.resources = append(m.resources, Resource{Kind: "stream", Name: app})
	}
	m.actions = func(r Resource) []Action {
		sel := fmt.Sprintf(`{app=%q}`, r.Name)
		return []Action{
			{Name: "Recent logs", Query: sel},
			{Name: "Errors only", Query: sel + ` |= "error"`},
			{Name: "Live tail", Query: "tail " + sel},
		}
	}
	m.query = func(ctx context.Context, q string) (Result, error) {
		if q == "labels" {
			return table([]string{"label", "values"}, [][]string{
				{"app", "api, worker, gateway"},
				{"env", "prod, staging"},
			}), nil
		}
		g := lokiQuery.FindStringSubmatch(q)
		if g == nil {
			return Result{}, unknown(q, `labels · {app="api"} · {app="api"} |= "error" · tail {app="worker"}`)
		}
		var lines []string
		for _, l := range lokiLines[g[2]] {
			if strings.Contains(l, g[3]) {
				lines = append(lines, l)
			}
		}
		if g[1] != "" && len(lines) > 0 {
			return Result{Summary: "tailing " + g[2], Stream: stream(ctx, 400*time.Millisecond, func(int) string {
				l := lines[rand.IntN(len(lines))]
				return fmt.Sprintf("%s %-5s %s", time.Now().Format("15:04:05"), logLevel(l), l)
			})}, nil
		}
		var rows [][]string
		now := time.Now()
		for i := range min(len(lines)*3, 12) {
			l := lines[i%len(lines)]
			at := now.Add(-time.Duration(12-i) * 7 * time.Second)
			rows = append(rows, []string{at.Format("15:04:05"), logLevel(l), l})
		}
		return table([]string{"time", "level", "line"}, rows), nil
	}
	return m
}
