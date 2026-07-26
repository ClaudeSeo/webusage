# WebUsage Repository Guide

## Purpose

WebUsage is a Go service that collects AI-tool usage metrics, stores snapshots in
SQLite, and serves a server-rendered dashboard plus JSON APIs.

The runtime supports two collection paths:

- `internal/openusage` reads the local OpenUsage HTTP API when
  `OPENUSAGE_ENABLED` is enabled.
- `internal/native` reads provider-specific local state. The registered native
  provider is currently `internal/native/kirocli`.

`cmd/server/main.go` is the composition root. It loads configuration, opens the
store, registers providers, and runs the collector and HTTP server under one
cancellable context.

## Repository Map

- `cmd/server/`: executable entry point and dependency wiring.
- `internal/`: application packages; see `internal/AGENTS.md` for package-level
  boundaries and invariants.
- `templates/`: Go HTML templates loaded by `internal/http`.
- `scripts/install.sh`: installation into the user-selected application path.
- `scripts/manage.sh`: pull, build, start, stop, restart, status, and log
  lifecycle commands.
- `data/`: default runtime SQLite location; `.gitkeep` preserves the directory.
- `.env.example`: documented runtime environment variables.
- `Makefile`: canonical development commands.

## Development Commands

The module requires Go `1.26.1`; `mise.toml` resolves the local Go toolchain.

```bash
make deps
make build
make dev
make test
make test-race
make coverage
make fmt
make lint
```

`make build` writes `./webusage`. `make build-prod` writes
`./webusage-linux` and `./webusage-macos`. `make coverage` writes
`coverage.out` and `coverage.html`.

Run the smallest relevant package test during iteration, then run `make test`
before handing off a behavior change. Use `make test-race` for collector,
concurrency, or shared-store changes.

## Runtime Rules

- Configuration is loaded from `.env` in the process working directory and then
  from environment variables.
- Defaults are `SERVER_HOST=127.0.0.1`, `SERVER_PORT=8080`,
  `DB_PATH=./data/usage.db`, `COLLECTION_INTERVAL=900`,
  `OPENUSAGE_URL=http://127.0.0.1:6736`, and
  `OPENUSAGE_ENABLED=true`.
- Set `OPENUSAGE_ENABLED=false` for native-only collection. A missing native
  provider is skipped rather than treated as a service failure.
- The collector runs once at startup and then on each collection interval.
- `SIGINT` and `SIGTERM` initiate shutdown through context cancellation, but
  current native I/O has no cancellation or finite timeout. Changes to native
  I/O must close that gap so shutdown is not blocked by an external request.
- Templates are parsed at server construction. Template names, template data,
  and client-side selectors form one contract and must change together.

## Change Boundaries

- Keep dependency construction in `cmd/server/main.go`; reusable behavior
  belongs in `internal`.
- Keep provider parsing and transport outside HTTP handlers. Handlers consume
  normalized domain and store data.
- Write all code comments and Go documentation comments in English.
- Preserve lowercase metric keys across collection, persistence, preferences,
  and rendering.
- Treat API response shapes and status codes as public contracts. Update the API
  contract tests with intentional changes.
- Do not commit runtime databases, WAL/SHM files, compiled binaries, coverage
  output, PID files, or logs.
- Do not edit generated artifacts to change behavior; change source and rebuild.

## Testing Expectations

- Add behavior tests beside the affected package using Go's standard `testing`
  package.
- HTTP behavior uses `httptest` and the real templates under `templates/`.
- Store and collector tests use temporary SQLite databases; never reuse
  `data/usage.db`.
- Keep network-dependent tests opt-in. The Kiro CLI live test runs only with
  `LIVE_TEST=true`; normal tests must use local HTTP or filesystem boundaries.
- For UI changes, verify both server-rendered HTML behavior and any JavaScript
  contract represented by tests under `internal/http`.
