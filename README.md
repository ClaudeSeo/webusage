# webusage

![Go Version](https://img.shields.io/badge/go-1.26%2B-00ADD8)

An OAuth-based AI subscription usage monitoring dashboard. Automatically discovers credentials from locally installed AI tools — no API keys or manual configuration required.

Inspired by [OpenUsage](https://github.com/robinebers/openusage).

## Quick Start

```bash
# Build
go build -o webusage ./cmd/server

# Run
./webusage

# Open dashboard
open http://127.0.0.1:8080
```

## How It Works

On startup, webusage scans your local machine for OAuth credentials left by AI tools you are already logged into. Any discovered provider is automatically enabled and begins collecting usage data on a configurable interval. No API keys, no manual token setup.

## Supported Providers

| Provider | Credential Source | Usage Endpoint |
|----------|------------------|----------------|
| Claude | `~/.claude/.credentials.json` + macOS Keychain (`Claude Code-credentials`) | `api.anthropic.com/api/oauth/usage` |
| GitHub Copilot | macOS Keychain (`gh:github.com`) via gh CLI | `api.github.com/copilot_internal/user` |
| Cursor | `~/Library/.../state.vscdb` (SQLite) + macOS Keychain | `api2.cursor.sh` |
| Gemini | `~/.gemini/oauth_creds.json` | Google OAuth2 |

If a provider's credentials are not found, it is silently skipped and the dashboard shows only the providers that were discovered.

## Prerequisites

- Go 1.26+ (or [mise](https://mise.jdx.dev/) with `go = "latest"`)
- At least one supported AI tool installed and logged in on your machine
- macOS (Keychain-based credential discovery is macOS-only)

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
make dev     # hot-reload via air (falls back to go run if air is not installed)
```

## Configuration

webusage reads configuration from environment variables. Copy `.env.example` to `.env` to get started.

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_HOST` | `127.0.0.1` | HTTP server bind address |
| `SERVER_PORT` | `8080` | HTTP server port |
| `DB_PATH` | `./data/usage.db` | SQLite database file path |
| `COLLECTION_INTERVAL` | `300` | Usage polling interval in seconds |
| `CURSOR_DB_PATH` | _(auto-detected)_ | Override path to Cursor's `state.vscdb` |
| `GEMINI_CRED_PATH` | `~/.gemini/oauth_creds.json` | Override path to Gemini OAuth credentials |

## API Reference

### GET /

Server-rendered HTML dashboard with Chart.js visualizations.

### GET /api/current

Current usage snapshot for all discovered providers.

```json
[
  {
    "provider": "claude",
    "metric": "tokens",
    "used": 142000,
    "collected_at": "2026-03-31T12:00:00Z"
  }
]
```

### GET /api/trends

Historical usage trends. Accepts a `range` query parameter.

```
GET /api/trends?range=24h
GET /api/trends?range=7d
GET /api/trends?range=30d
```

### GET /api/providers

List of all registered providers and their current status.

### GET /healthz

Health check endpoint. Returns `200 OK` when the server is ready.

## Project Structure

```
webusage/
├── cmd/
│   └── server/
│       └── main.go               # Entry point: provider discovery, collector, HTTP server
├── internal/
│   ├── collector/
│   │   └── collector.go          # Scheduled collection with retry/backoff
│   ├── credfinder/
│   │   ├── jsonfile.go           # JSON credential file reader
│   │   ├── keychain.go           # macOS Keychain reader
│   │   ├── sqlite.go             # SQLite credential reader (Cursor)
│   │   └── jwt.go                # JWT parsing utilities
│   ├── http/
│   │   └── routes.go             # HTTP route registration
│   ├── oauth/
│   │   ├── oauth.go              # OAuth2 token refresh flow
│   │   ├── store.go              # Token persistence interface
│   │   └── token.go              # Token model (expiry, refresh logic)
│   ├── provider/
│   │   ├── provider.go           # Provider interface
│   │   ├── registry.go           # Provider registry
│   │   ├── types.go              # Shared types (UsagePoint, SubscriptionInfo)
│   │   ├── claude/               # Claude provider
│   │   ├── copilot/              # GitHub Copilot provider
│   │   ├── cursor/               # Cursor provider
│   │   └── gemini/               # Gemini provider
│   ├── stats/
│   │   └── stats.go              # Usage aggregation
│   └── store/
│       ├── store.go              # SQLite store (WAL mode)
│       ├── usage.go              # Usage snapshot persistence
│       └── providers.go          # Provider record management
├── templates/
│   ├── layout.html               # Base HTML layout
│   ├── dashboard.html            # Dashboard page
│   └── components/               # Reusable template partials
├── data/                         # SQLite database directory (gitignored)
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

For hot-reload during development, install [air](https://github.com/air-verse/air):

```bash
go install github.com/air-verse/air@latest
make dev
```

## Collector Behavior

- Runs immediately on startup, then on every `COLLECTION_INTERVAL` tick
- Each provider runs in its own goroutine with an atomic lock to prevent duplicate runs
- Failed collections retry up to 3 times with exponential backoff and random jitter (capped at 5 minutes)
- Token refresh is handled inside each provider's `FetchUsage` call — the collector never touches credentials directly
- Usage snapshots are stored idempotently to avoid duplicates on retry
