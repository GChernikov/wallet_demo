# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Purpose

Benchmark service comparing two MySQL locking strategies for high-throughput balance updates:
- **SELECT FOR UPDATE** (pessimistic): locks row for transaction duration
- **Optimistic locking**: version-based CAS with retry loop (max retries → HTTP 409)

Load profiles: 3000 RPS total — either 750 wallets × 4 RPS (low contention) or 100 wallets × 30 RPS (high contention).

## Commands

```bash
make up            # Start MySQL + app via docker compose
make down          # Stop everything
make build         # go build ./...
make seed          # Insert load test wallets (run after make up)
make test          # go test -race ./...
make lint          # golangci-lint run ./...

# Run a load test scenario:
make loadtest SCENARIO=750-wallets-4rps-sfu
make loadtest SCENARIO=750-wallets-4rps-optimistic
make loadtest SCENARIO=100-wallets-30rps-sfu
make loadtest SCENARIO=100-wallets-30rps-optimistic
make loadtest SCENARIO=diagnostics-noop
make loadtest SCENARIO=diagnostics-db-ping

# Direct k6:
k6 run loadtest/750-wallets-4rps-sfu.js -e BASE_URL=http://localhost:8080
```

Migrations run automatically on application startup (goose embedded).

## Architecture

```
cmd/balance-api/main.go          HTTP server + wire-up + migrations on start
internal/
  model/balance.go               Request/Response types, ConflictError
  repository/mysql/
    db.go                        sql.DB factory with pool config
    stmts.go                     All prepared statements (compiled once at startup)
  service/
    service.go                   BalanceService interface
    select_for_update.go         Pessimistic: BEGIN → SELECT FOR UPDATE → UPDATE → INSERT × 2 → COMMIT
    optimistic.go                Optimistic: read → BEGIN → UPDATE WHERE version=? → retry if 0 rows → INSERT × 2 → COMMIT
  migrations/
    embed.go                     Embeds SQL files for goose
    00001_init.sql               Schema (balances, transactions, outbox) + fixture-account
cmd/seed/main.go                 Bulk-inserts load test wallets (sfu-*/ol-* prefixes)
loadtest/                        k6 scenarios
monitoring/mysql-top-sql.sh      performance_schema query profiling
```

## Key Design Decisions

**No RETURNING in MySQL** — after UPDATE, new balance is computed in Go (old + amount), not read back from DB.

**Prepared statements** — all SQL compiled in `PrepareStatements()` at startup; within transactions use `tx.StmtContext(ctx, stmt)`.

**Optimistic locking conflict detection** — `RowsAffected() == 0` on the version-checked UPDATE means another writer won the race.

**MySQL tuning** — `innodb-flush-log-at-trx-commit=2` (no fsync per commit, like PostgreSQL `synchronous_commit=off`), large buffer pool and redo log, `max-connections=500`.

## HTTP API

| Method | Path | Description |
|--------|------|-------------|
| POST | `/balances/update/select-for-update` | Pessimistic locking |
| POST | `/balances/update/optimistic` | Optimistic locking + retry |
| GET | `/diagnostics/noop` | HTTP baseline |
| GET | `/diagnostics/db-ping` | `SELECT 1` baseline |
| GET | `/check` | Health check |

Request: `{"account_id": "sfu-750-user-1", "amount": 100}`
Response: `{"account_id": "...", "balance": ..., "mode": "...", "transaction_id": "...", "outbox_event_id": "..."}`
409 Conflict: when optimistic locking exhausts retries (`OL_MAX_RETRIES`, default 5).

## Environment Variables

| Variable | Default                                                      | Description |
|----------|--------------------------------------------------------------|-------------|
| `HTTP_PORT` | `8080`                                                       | HTTP listen port |
| `DB_DSN` | `root@tcp(localhost:3306)/wallet_demo?parseTime=true` | MySQL DSN |
| `DB_MAX_OPEN_CONNS` | `140`                                                        | Connection pool size |
| `DB_MAX_IDLE_CONNS` | `140`                                                        | Idle connections |
| `DB_CONN_MAX_LIFETIME` | `5m`                                                         | Connection max lifetime |
| `OL_MAX_RETRIES` | `5`                                                          | Optimistic locking retry limit |

## Load Test Wallet Prefixes

- `fixture-account` — manual testing (seeded in migration)
- `sfu-750-user-{1..750}` — SELECT FOR UPDATE, low contention scenario
- `ol-750-user-{1..750}` — Optimistic, low contention scenario
- `sfu-100-user-{1..100}` — SELECT FOR UPDATE, high contention scenario
- `ol-100-user-{1..100}` — Optimistic, high contention scenario
