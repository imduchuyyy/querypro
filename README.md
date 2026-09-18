# querypro

A terminal UI for backend developers to explore and operate backend stacks
(Postgres, MongoDB, Redis, RabbitMQ, Kafka, Loki) from one place.

querypro is plugin-first: the Go core owns the TUI, plugin lifecycle,
connections and storage, and knows nothing about any datastore. Every backend
is a TypeScript plugin in `plugins/` that runs as a subprocess and talks to
the core over gRPC, with streaming for live data (tails, subscriptions,
consumers).

## Requirements

- Go (version in `go.mod`) to build
- Node.js 24.2+ on `PATH` at runtime (plugins run TypeScript directly)
- Docker, only for `make demo` and the integration tests

## Usage

```sh
make build && ./bin/querypro     # or: make run
make demo                        # start every backend in docker, seeded tabs
```

`ctrl+t` opens a connection, `enter` runs a query, `ctrl+o` opens a resource,
`ctrl+x` shows its actions, `/` opens the command palette, `!name query` runs
against another tab. The palette's Help lists every key.

| Plugin   | URI                                                   | Queries                                                     |
| -------- | ----------------------------------------------------- | ----------------------------------------------------------- |
| postgres | `postgres://user:pass@host:5432/db?sslmode=require`   | SQL, `\d name`, `\dt`, `\dn`, `\l`                          |
| mongodb  | `mongodb://host:27017/db`                             | mongo shell: `db.x.find({})`, `use db`, `show collections`  |
| redis    | `redis://:pass@host:6379/0`, `rediss://…`             | any command, `SUBSCRIBE`/`PSUBSCRIBE`/`MONITOR` stream live |
| rabbitmq | `amqp://user:pass@host:5672/vhost?management=http://host:15672` | `queues`, `peek q`, `tap exchange #`, `publish`, …  |
| kafka    | `kafka://user:pass@h1:9092,h2:9092?ssl=true&mechanism=scram-sha-512` | `topics`, `describe t`, `tail t`, `produce t v`, … |
| loki     | `https://user:pass@host?org=tenant`                   | LogQL, `since 6h {app="x"}`, `tail {app="x"}`, `labels`     |

Unknown commands reply with the list of supported ones.

## Files

Everything lives in `$QUERYPRO_HOME`, default `os.UserConfigDir()/querypro`
(`~/Library/Application Support/querypro` on macOS, `~/.config/querypro` on
Linux). The directory is `0700` and every file `0600`.

| File               | Contents                                            |
| ------------------ | --------------------------------------------------- |
| `connections.json` | saved connections, including their URIs and secrets |
| `settings.json`    | theme, sidebar, mouse                               |
| `history.json`     | last 500 queries                                    |
| `logs/<kind>.log`  | plugin stderr, truncated past 10 MB                 |

Writes are atomic (temp file + rename). A corrupt file stops startup with its
path instead of being overwritten.

Plugins are found in `-plugins`, `$QUERYPRO_PLUGINS`, `plugins/` next to the
binary, or `./plugins`.

## Development

```sh
make tools      # pinned golangci-lint, buf, protoc-gen-go(-grpc)
make test       # go test -race + plugin unit tests
make it         # integration tests, needs `make services`
make lint       # golangci-lint, tsc, buf lint
make proto      # regenerate internal/plugin/pb from proto/
make dist       # dist/querypro-<version>-<os>-<arch>.tar.gz
```

A release is the binary plus `plugins/` (with production `node_modules`) and
`proto/` side by side; `make dist` builds it for the current `GOOS/GOARCH`.

## Writing a plugin

A plugin is a directory in `plugins/` with a `plugin.json`:

```json
{
  "kind": "postgres",
  "code": "PG",
  "color": "#5b9bd5",
  "uri": "postgres://postgres:postgres@localhost:5432/postgres",
  "placeholder": "SELECT * FROM users LIMIT 10;",
  "command": ["node", "index.ts"]
}
```

and an entry point that calls `serve` from `plugins/sdk`:

```ts
import { serve, table, type Session } from "../sdk/index.ts";

serve(async (uri): Promise<Session> => ({
  server: "Example 1.0",
  resources: async () => [{ kind: "table", name: "users" }],
  actions: (r) => [{ name: "Preview", query: `SELECT * FROM ${r.name}` }],
  query: async (q, signal) => table(["q"], [[q]]),
  close: async () => {},
}));
```

`query` returns `table(...)`, `text(...)`, or `live(signal, summary, start)`
for streams; `dispatch` maps small command languages. Any executable that
speaks the protocol below works; the SDK is just the TypeScript way.

### Protocol

The contract is `proto/querypro/plugin/v1/plugin.proto`.

1. The core starts `command` in the plugin directory with
   `QUERYPRO_SOCKET=<path>` and a stdin pipe.
2. The plugin serves `PluginService` on that unix socket and then writes
   `querypro-plugin 1\n` to stdout. Anything else fails the start.
3. `Connect(uri)` returns a session id used by every other call.
4. `Query` streams one `table` or `text` event for a finished result, or
   one `live` event followed by `line` events until the core cancels.
5. The plugin exits when its stdin closes. If it dies, the core marks its
   tabs disconnected and respawns it on the next connect.
