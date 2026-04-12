# webusage

![Go Version](https://img.shields.io/badge/go-1.26%2B-00ADD8)
![Version](https://img.shields.io/badge/version-1.0.0-blue)

A lightweight AI usage monitoring dashboard that displays usage data from OpenUsage API. No credential management required — just connect to your OpenUsage instance.

## Architecture

```
OpenUsage App → /v1/usage API → webusage → SQLite → Dashboard
```

webusage focuses on the **view layer** — all credential handling and provider parsing is delegated to [OpenUsage](https://github.com/robinebers/openusage).

## Quick Start

```bash
# Prerequisite: OpenUsage must be running
# See: https://github.com/robinebers/openusage

# Build
go build -o webusage ./cmd/server

# Run (default: connects to OpenUsage at http://127.0.0.1:6736)
./webusage

# Open dashboard
open http://127.0.0.1:8080
```

## How It Works

1. **OpenUsage** collects usage data from your AI tools (Claude, Codex, Copilot, etc.)
2. **webusage** fetches data from OpenUsage's `/v1/usage` API
3. Data is stored in SQLite and displayed in a Vercel-style dashboard

## Prerequisites

- Go 1.26+ (or [mise](https://mise.jdx.dev/) with `go = "latest"`)
- **OpenUsage** running at `http://127.0.0.1:6736` (or configure `OPENUSAGE_URL`)

## Installation

```bash
git clone https://github.com/ClaudeSeo/webusage.git
cd webusage
go mod download
```

## Building

```bash
# Development build
make build        # outputs ./webusage

# Production builds (CGO_ENABLED=0, stripped)
make build-prod   # outputs ./webusage-linux and ./webusage-macos
```

The project uses [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — a pure Go SQLite driver with no CGO dependency.

## Running

```bash
make run     # runs ./webusage
make dev     # runs via go run
```

## Managed Run

`scripts/manage.sh` handles the full lifecycle: pull, build, start, stop, and log tailing.

```bash
./scripts/manage.sh           # git pull + build + start in background (same as "start")
./scripts/manage.sh start     # git pull + build + start in background
./scripts/manage.sh stop      # stop the running process
./scripts/manage.sh restart   # restart without rebuilding
./scripts/manage.sh status    # check whether the process is running
./scripts/manage.sh logs      # tail -f the log file
./scripts/manage.sh update    # git pull + rebuild + restart (alias for start)
```

Runtime data (database, PID file, log file) is written to `~/.webusage/` by default. Set `WEBUSAGE_HOME` to use a different directory.

## Configuration

webusage reads configuration from environment variables. Place your `.env` file in `$WEBUSAGE_HOME` (default: `~/.webusage/.env`).

| Variable | Default | Description |
|----------|---------|-------------|
| `WEBUSAGE_HOME` | `~/.webusage` | Directory for runtime data (DB, PID, logs) |
| `SERVER_HOST` | `127.0.0.1` | HTTP server bind address |
| `SERVER_PORT` | `8080` | HTTP server port |
| `DB_PATH` | `$WEBUSAGE_HOME/usage.db` | SQLite database file path (overrides `WEBUSAGE_HOME`) |
| `COLLECTION_INTERVAL` | `900` | Usage polling interval in seconds (15 min default) |
| `OPENUSAGE_URL` | `http://127.0.0.1:6736` | OpenUsage API endpoint |

## API Reference

### GET /

Server-rendered HTML dashboard with Chart.js visualizations.

### GET /api/current

Current usage snapshot for all active providers.

```json
{
  "claude": {
    "provider_id": "claude",
    "display_name": "Claude",
    "cycle_type": "rolling_5h",
    "limit_type": "limited",
    "current_usage": 68.0,
    "limit_value": 100,
    "usage_percent": 68.0,
    "time_remaining": "3h 26m"
  }
}
```

### GET /api/trends

Historical usage trends. Accepts `range` and `provider_id` query parameters.

```
GET /api/trends?range=24h                    # All providers, 24 hours
GET /api/trends?provider_id=claude&range=7d  # Claude only, 7 days
```

### GET /api/forecast

Usage forecast for all providers based on current pace.

### GET /api/providers

List of all registered providers and their status.

### POST /api/providers/:name/enable

Enable a provider.

### POST /api/providers/:name/disable

Disable a provider.

### POST /api/collect

Trigger immediate data collection from OpenUsage.

### GET /healthz

Health check endpoint. Returns `200 OK` when the server is ready.

## Project Structure

```
webusage/
├── cmd/
│   └── server/
│       └── main.go               # Entry point: OpenUsage client, collector, HTTP server
├── internal/
│   ├── collector/
│   │   └── collector.go          # Scheduled collection from OpenUsage API
│   ├── domain/
│   │   ├── cycle.go              # Cycle types (rolling_5h, daily, weekly, monthly)
│   │   └── cycle_helpers.go       # Cycle calculation utilities
│   ├── http/
│   │   ├── server.go             # HTTP server setup
│   │   ├── cycle_handlers.go     # Cycle-aware API handlers
│   │   └── server_test.go        # HTTP tests
│   ├── openusage/
│   │   └── client.go             # OpenUsage HTTP API client
│   ├── stats/
│   │   └── stats.go              # Usage aggregation
│   └── store/
│       ├── store.go              # SQLite store (WAL mode)
│       ├── usage.go              # Usage snapshot persistence
│       └── providers.go           # Provider record management
├── templates/
│   ├── layout.html               # Base HTML layout (Vercel-style)
│   ├── dashboard.html            # Dashboard page
│   └── components/               # Reusable template partials
│       ├── provider_card.html    # Provider usage card
│       ├── trend_chart.html      # Chart.js trend visualization
│       └── error_state.html      # Error display component
├── scripts/
│   └── manage.sh                 # Lifecycle management (start/stop/restart/status/logs/update)
├── go.mod
├── Makefile
└── mise.toml
```

## Development

```bash
make test        # Run all tests
make test-race   # Run tests with race detector
make coverage    # Generate HTML coverage report (coverage.html)
make fmt         # Format all Go source files
make lint        # Run golangci-lint
make clean       # Remove build artifacts and database files
```

## Collector Behavior

- Runs immediately on startup, then on every `COLLECTION_INTERVAL` tick (default: 15 minutes)
- Fetches all provider data from OpenUsage `/v1/usage` endpoint
- Stores usage snapshots idempotently to avoid duplicates
- Automatically registers new providers discovered via OpenUsage
- Metric names are normalized to lowercase for consistency

## License

MIT