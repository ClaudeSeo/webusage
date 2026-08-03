# Internal Package Guide

This file adds rules specific to `internal/`; repository-wide commands and
runtime defaults are defined by the parent guide.

## Package Map

- `config`: environment and `.env` parsing into the runtime `Config`.
- `domain`: cycle calculations, provider view models, and metric preference
  reconciliation. Keep these rules independent of transport and persistence.
- `openusage`: typed client for the OpenUsage `/v1/usage` API.
- `native`: provider interface, normalized metric type, and registry.
- `native/kirocli`: read-only Kiro CLI credential lookup, live usage request,
  and response-to-metric mapping.
- `native/ollama`: Ollama Cloud usage request keyed by `OLLAMA_API_KEY` and
  response-to-metric mapping. It has no local state; the key is injected by the
  composition root.
- `collector`: coordinates OpenUsage and native collection, provider locking,
  normalization, idempotent persistence, and job state.
- `store`: SQLite schema and queries for providers, snapshots, and metric
  preferences.
- `http`: route registration, handlers, template composition, and API/view
  response shaping.
- `stats`: pure trend and aggregation helpers.

## Dependency Direction

- `domain` and `stats` must not depend on HTTP, collectors, or storage.
- Provider implementations satisfy `native.Provider` and return
  `native.Metric`; they do not write to `store` directly.
- `collector` owns the conversion from external/native values to
  `store.UsageSnapshot`.
- `http` may orchestrate `collector`, `domain`, `openusage`, and `store`, but
  persistence and provider parsing stay in their owning packages.
- Add concrete providers to the registry in `cmd/server/main.go`, not by making
  `native` import its implementations.

## Collection and Storage Invariants

- Native providers are independent: unavailable providers are skipped and one
  provider error must not abort other collection paths.
- OpenUsage errors retain their caller-visible error contract; native errors are
  recorded and logged per provider.
- Persistence is serialized per provider, but native `Collect` runs before the
  lock. Do not assume collection is serialized; close this gap in concurrency changes.
- `persistSnapshots` assigns provider IDs and collection timestamps for both
  collection paths.
- Metric keys are normalized lowercase identifiers. A provider must aggregate
  duplicate logical metrics before persistence.
- Snapshot idempotency is the tuple
  `(provider_id, metric, collected_at)` after timestamps are truncated to one
  second.
- SQLite remains single-connection with WAL, `busy_timeout=5000`, foreign keys,
  and transactional multi-row writes.
- Metric preference updates use versioned compare-and-swap behavior and must
  remain atomic across a submitted batch.

## Security and External I/O

- `native/kirocli` opens
  `~/Library/Application Support/kiro-cli/data.sqlite3` in read-only mode.
  WebUsage must never refresh, overwrite, or delete Kiro CLI credentials.
- Access and refresh tokens must never enter logs, errors, stored `RawJSON`, test
  failure output, or HTTP responses.
- Send the access token only in the `Authorization` header for
  `https://management.<region>.kiro.dev/Get-Usage-Limits`.
- Derive the management region from `profileArn`; do not substitute the token's
  identity-center region.
- `native/ollama` sends `OLLAMA_API_KEY` only as a bearer token to the fixed
  `https://ollama.com/api/usage` endpoint. The key must never enter logs,
  errors, stored `RawJSON`, test failure output, or HTTP responses. Keep the
  host hardcoded and keep using the stdlib redirect policy, which drops
  `Authorization` on a redirect to a different domain; `ollama.com` subdomains
  still receive it.
- `native.Provider.Collect` receives the collector context, and every native
  HTTP client sets a finite timeout. New or changed external I/O must preserve
  both; use `httptest.Server` unless `LIVE_TEST=true`.

## Test Deltas

- `http` tests construct `NewServer` with `../../templates`; keep fixtures and
  template paths aligned with that package working directory.
- `collector` tests use fake `native.Provider` implementations plus a temporary
  real store to verify persistence, availability, errors, and idempotency.
- `store` tests assert schema settings, transactions, cascades, and concurrent
  compare-and-swap behavior against temporary SQLite files.
- `native/kirocli` tests must cover request host/path/headers, token expiry,
  response aggregation, reset-time fallbacks, and absence of credential data in
  persisted raw responses.
- `native/ollama` tests must cover request URL/headers, rejected-key handling,
  ratio-to-percent scaling, unreported windows staying unpersisted, context
  cancellation, and absence of the API key in errors and raw responses.
