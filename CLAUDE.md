# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
make build                          # npm ci (if needed) + go build -o bin/ ./...
make run                            # run the TUI
make demo                           # docker compose backends + seeded tabs
make test                           # go test -race ./... + plugin node tests
make it                             # integration tests (make services first)
go test ./path/to/pkg -run TestName # single Go test
cd plugins && node --test redis/index.test.ts   # single plugin test
make lint                           # golangci-lint v2, tsc, buf lint
make fmt                            # gofmt + goimports via golangci-lint
make proto                          # regenerate internal/plugin/pb
make tools                          # install pinned lint/proto tools
make dist                           # release tarball for GOOS/GOARCH
```

CI (`.github/workflows/ci.yml`) runs test, integration (docker compose) and
lint, and fails if `make proto` changes generated code. Tool versions are
pinned in the `Makefile`; the golangci-lint version is also in the workflow,
bump them together.

## Architecture

querypro is a TUI for many backends (Postgres, MongoDB, Redis, RabbitMQ,
Kafka, Loki). It is **plugin-first**: the Go core knows nothing about any
datastore; every backend is a TypeScript plugin in `plugins/` running as a
subprocess and talking gRPC over a unix socket.

- `main.go`: resolves the home dir and plugin dir, builds the host, runs TUI.
- `proto/querypro/plugin/v1/plugin.proto`: **the core/plugin contract**.
  Every plugin depends on it; change it deliberately, run `make proto`.
- `internal/plugin`: `Host` discovers `plugins/*/plugin.json`, spawns a
  plugin lazily on first `Connect`, waits for the `querypro-plugin 1`
  handshake line on stdout, and reports crashes on `Exits()`. Stderr goes to
  `$QUERYPRO_HOME/logs/<kind>.log`. Closing stdin stops a plugin. `session`
  adapts the gRPC client to the `Session` interface; a `Query` stream's first
  event decides the result (table, text, or live then lines).
- `internal/store`: JSON files in `$QUERYPRO_HOME` with atomic 0600 writes.
- `internal/tui`: Bubble Tea UI, see below.
- `plugins/sdk/index.ts`: `serve(connect)` implements the gRPC side;
  helpers `table`, `text`, `live`, `dispatch`, `words`, `quote`.
- `plugins/<kind>/index.ts` + `plugin.json`: one plugin per backend.

Not built yet (YAGNI until needed): plugin-contributed keybindings, a
separate secrets store (URIs with passwords live in `connections.json`),
a generic event bus (the Bubble Tea message loop plays that role).

## Plugins (`plugins/`)

One npm package (`plugins/package.json`) holds every plugin's deps. Node
24.2+ runs the `.ts` files directly via type stripping, so there is no build
step: use only erasable TypeScript (no enums, namespaces or parameter
properties), import local files with `.ts` extensions, and start the server
under `if (import.meta.main)` so tests can import the module. `npm run check`
typechecks.

- `Session.actions(resource)` returns named queries; the first is what
  opening a resource runs. Mark destructive ones `danger: true`.
- `live(signal, summary, start)` must finish subscribing inside `start` so
  errors surface as query errors, and return a stop function.
- Command languages use `dispatch` with keys like `"tail <topic> [from]"`:
  `<arg>` is required, `[arg]` optional, and usage errors come for free.
- The SDK caps tables at 1000 rows, cells at 500 chars, text at 1 MB.
- Integration tests for every plugin live in `internal/plugin/host_test.go`
  (`TestBackends`); new plugins and actions belong there.

## TUI (`internal/tui`)

Bubble Tea v2 + Lip Gloss v2 + Bubbles v2 (all `charm.land/...` imports,
not `github.com/charmbracelet/...`). Layout: tabline on top, sidebar on the
left with the current tab's connection and resources, output viewport,
titled prompt box and a mode statusline at the bottom, and modal dialogs
composited over the screen with `lipgloss.NewCompositor`.
The look is deliberately *not* a copy of opencode: teal/amber `querypro`
theme, per-backend colored badges (code and color come from each
`plugin.json`), and `titled` rounded boxes whose label sits in the top border.

Files: `app.go` (model, Update, query execution, `!target`), `conn.go`
(connection lifecycle, tabs, profiles), `commands.go` (palette and list
dialogs), `persist.go` (store files), `view.go`, `dialog.go`, `mouse.go`,
`theme.go`.

- `model` is used as a pointer; `Update` runs `handle` then `layout`, which
  sizes every widget from the rendered heights of the prompt and footer.
- The TUI depends on the small `backend` interface, not on `*plugin.Host`;
  tests use `fakeHost`/`fakeSession` in `app_test.go` and `do(m, cmd)` to
  run commands synchronously.
- Every plugin call is async: `connect`, `refresh` and `fetchActions` return
  a `tea.Cmd` producing `connectedMsg`, `resourcesMsg` or `actionsMsg`.
  `connection.attempt` drops stale connect results. A tab is connected when
  `sess != nil`; running a query on a disconnected tab starts a reconnect.
- One tab per connection. Each `connection` owns its `entries`, input
  `draft` and scroll `offset`; always change tabs via `switchTab`. Queries
  and streams keep running in background tabs; `esc` stops only the current
  tab. Entries cache their rendered view in `view`/`viewKey`.
- Saved connections (`m.profiles`, `connections.json`) are separate from
  open tabs (`m.conns`): closing a tab keeps the profile. `openProfile`
  switches to an existing tab instead of duplicating it, because `!name`
  resolves tabs by name. `checkName` requires a unique name with no spaces
  or `!`.
- Dialogs implement the `dialog` interface. Opening one is a message:
  `open(d)` returns a `tea.Cmd` whose message is the dialog itself.
  `listDialog` backs the palette, settings, switcher and help; items are
  rebuilt on every render so values like the theme stay live. Danger
  actions and deletes go through `confirm`.
- Lip Gloss v2 `Width`/`Height` include border and padding.
- Mouse (`mouse.go`): the tabline and sidebar append clickable `area`s to
  `m.clicks` on every render. Drag in the output selects in content
  coordinates, and release copies via OSC52 plus the OS clipboard.
- `!<target> <query>` runs against another connection without switching.
  `resolve` tries exact name, then kind or badge code (`mongodb`, `MG`), then
  unique prefix of name or kind; ambiguity is an error, never a guess.
- Tab completes the target while the input is `!<partial>` (no space yet),
  cycling on repeat via `m.comp`; otherwise tab switches connection.
  `flash` shows a short-lived pre-styled notice in the statusline; `fail`
  flashes an error.
