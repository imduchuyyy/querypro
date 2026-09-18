# querypro

A terminal UI for backend developers to explore and operate backend stacks
(MongoDB, Postgres, Redis, RabbitMQ, Kafka, Loki, ...) from one place.

querypro is plugin-first: a small core handles plugin lifecycle, navigation,
connections, secrets, keybindings, and an event bus. Each datastore is a
plugin running as a subprocess that talks to the core over gRPC, with
streaming for live data such as log tails and consumers.

> Status: early development.

## Development

Requires Go (version in `go.mod`).

```sh
make lint-install   # install golangci-lint
make build          # build
make run            # run the TUI
make demo           # run with mock connections for every backend
make test           # run tests with the race detector
make lint           # lint
make fmt            # format code
```

Run a single test: `go test ./path/to/pkg -run TestName`.

CI (`.github/workflows/ci.yml`) runs build, test, and lint on pushes to
`main` and on pull requests.
