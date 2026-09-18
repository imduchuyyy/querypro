# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Early stage: only the TUI shell in `internal/tui` exists (module `querypro`,
Go 1.25). The core/plugin architecture below is the **intended design**, not
existing code. Update this file as real packages and proto definitions land.

## Commands

```sh
make build                          # go build -o bin/ ./...
make run                            # run the TUI
make demo                           # TUI seeded with mock connections
make test                           # go test -race ./...
go test ./path/to/pkg -run TestName # single test
make lint                           # golangci-lint v2 (.golangci.yml)
make fmt                            # gofmt + goimports via golangci-lint
make lint-install                   # install pinned golangci-lint
```

CI (`.github/workflows/ci.yml`) runs build, test, and lint. The
golangci-lint version is pinned in both the `Makefile` and the workflow;
bump them together.

## Architecture (intended)

querypro is a Terminal UI for backend developers to work with many backend
stacks (MongoDB, Postgres, Redis, RabbitMQ, Kafka, Loki, ...). It is
**plugin-first**: the core knows nothing about any specific datastore.

### Core

The core process owns everything shared across plugins:

- **Plugin lifecycle**: discover, spawn, health-check, and stop plugins.
- **UI navigation/routing**: screens and routes between views.
- **Resources**: registry of what plugins expose.
- **Connections and secrets**: connection configs and their credentials.
- **Keybindings**: global plus plugin-contributed bindings.
- **Event bus**: decoupled communication between core components and plugins.

### Plugins

Each plugin is a datastore integration that contributes:

- **Connect method**: how to connect/authenticate to its backend.
- **Resources**: the browsable entities (databases, tables, collections, keys,
  topics, queues, log streams, ...).
- **Views**: how a resource is rendered in the TUI.
- **Actions**: operations a user can run on a resource.

### Plugin transport

Plugins run as **separate subprocesses** and talk to the core over **gRPC**,
with **streaming** support for continuous data (log tails, Kafka consumers,
pub/sub, etc.). The core/plugin contract is the gRPC service definition;
change it deliberately, since every plugin depends on it.

## TUI (`internal/tui`)

Bubble Tea v2 + Lip Gloss v2 + Bubbles v2 (all `charm.land/...` imports,
not `github.com/charmbracelet/...`). Layout: tabline on top, sidebar on the left
with the current tab's connection and resources, output viewport, titled
prompt box and a mode statusline at the bottom, and modal dialogs
composited over the screen with `lipgloss.NewCompositor`.
The look is deliberately *not* a copy of opencode: teal/amber `querypro`
theme, per-backend colored badges (`kinds` in `theme.go`), and `titled`
rounded boxes whose label sits in the top border.

- `model` is used as a pointer; `Update` runs `handle` then `layout`, which
  sizes every widget from the rendered heights of the prompt and footer.
- One tab per connection (vim-style tabline on top). Each `connection` owns
  its `entries`, input `draft` and scroll `offset`; always change tabs via
  `switchTab`, which saves and restores them. Queries and streams keep
  running in background tabs; `esc` stops only the current tab.
- Saved connections (`m.profiles`) are separate from open tabs (`m.conns`):
  closing a tab keeps the profile. `newTab` lists profiles plus
  "+ New connection"; `openProfile` switches to an existing tab instead of
  duplicating it, because `!name` resolves tabs by name. `checkName`
  requires a unique name with no spaces or `!`.
- Dialogs implement the `dialog` interface. Opening one is a message:
  `open(d)` returns a `tea.Cmd` whose message is the dialog itself.
- `listDialog` backs the command palette, settings, switcher and help;
  items are rebuilt on every render so values like the theme stay live.
- Lip Gloss v2 `Width`/`Height` include border and padding.
- Mouse (`mouse.go`): the tabline and sidebar append clickable `area`s to
  `m.clicks` on every render. Drag in the output selects in
  content coordinates (stable while scrolling), and release copies via
  OSC52 (`tea.SetClipboard`) plus the OS clipboard.
- Profiles and tabs live only in memory for now.
- Queries run async: `run` returns a `tea.Cmd` producing `resultMsg`; a
  `Result.Stream` channel is drained one line per `lineMsg` via `wait`, and
  `esc` cancels the entry's context. Entries are addressed by `id`, not index.
- `!<target> <query>` runs against another connection without switching.
  `resolve` tries exact name, then kind or badge code (`mongodb`, `MG`), then
  unique prefix of name or kind; ambiguity is an error, never a guess.
- Tab completes the target while the input is `!<partial>` (no space yet),
  cycling on repeat via `m.comp`; otherwise tab switches connection.
  `flash` shows a short-lived pre-styled notice in the statusline.

## Plugin contract (`internal/plugin`)

`plugin.Plugin` is the in-process stand-in for the future gRPC contract:
`Resources`, `Actions(resource)`, `Query(ctx, q)` and a `Placeholder`.
Actions are just named queries (`Action.Query`), so every action is visible
and re-runnable as text; `Danger` actions go through a confirm dialog.
`plugin.Mock(kind)` returns canned backends (SQL subset, mongo shell, redis
commands, small DSLs for rabbitmq/kafka, LogQL for loki); they never mutate.
`mock_test.go` runs every action of every mock, so new mock actions must
parse.
