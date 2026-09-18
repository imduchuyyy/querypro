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
not `github.com/charmbracelet/...`). Layout: connections sidebar on the left,
output viewport, titled prompt box and a mode statusline at the bottom, and
modal dialogs composited over the screen with `lipgloss.NewCompositor`.
The look is deliberately *not* a copy of opencode: teal/amber `querypro`
theme, per-backend colored badges (`kinds` in `theme.go`), and `titled`
rounded boxes whose label sits in the top border.

- `model` is used as a pointer; `Update` runs `handle` then `layout`, which
  sizes every widget from the rendered heights of the prompt and footer.
- Dialogs implement the `dialog` interface. Opening one is a message:
  `open(d)` returns a `tea.Cmd` whose message is the dialog itself.
- `listDialog` backs the command palette, settings, switcher and help;
  items are rebuilt on every render so values like the theme stay live.
- Lip Gloss v2 `Width`/`Height` include border and padding.
- Connections live only in memory, and queries return a "no plugin" error
  until the plugin system exists.
