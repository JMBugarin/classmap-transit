# classmap-transit

Versioned Santa Cruz METRO ingestion, replay and departures API for [Classmap](https://github.com/JMBugarin/ucsc-campus-router-v2). Go, PostgreSQL 17 and pgx. A small React operator page lives in `web/`.

Data: Santa Cruz METRO / SCMTD, CC BY 4.0 plus the developer agreement at https://developer.scmetro.org/. No endorsement. A missing prediction does not mean a cancellation.

## Architecture

```
poller (>=30s) ─┐                       ┌─ worker pool (1..16, leased, fenced)
transit ingest ─┼─> Accept (one tx) ────┤
                │   capture + fetch +   └─> Process (one tx): token check under FOR UPDATE,
                │   job, queue bound          decode, order/conflict policy, observations,
                │                             feed_state pointer, outcome, job done
transit replay ─┘   namespace replay-*  ──> same Process path, never touches ns=live
HTTP API ──> feed_state(live) ─> observations | scheduled fallback (GTFS static, service-day aware)
```

- **Atomic acceptance.** `Accept` writes the raw capture (SHA-256 deduplicated per namespace), a fetch row and a job in one transaction, under an advisory lock that holds the queue to 1000 open jobs. A crash leaves either all three rows or none.
- **Leases and fencing.** `Claim` uses `FOR UPDATE SKIP LOCKED`, bumps a fencing token, and sets a 30 s lease. `Process` locks the job row again and checks token, state and lease before any write, inside the transaction that publishes. A worker whose lease expired and was reclaimed can't publish. Retries back off exponentially with jitter, and jobs fail after five attempts. Operators can requeue failed jobs through an authenticated admin endpoint.
- **Publication policy.** Only GTFS-rt 2.0 `FULL_DATASET` snapshots with a header timestamp and unique, non-deleted entity ids are published. Older source times are recorded as `late`. Equal source time with the same hash is `duplicate`, with a different hash it's `conflict`, and future-dated or undecodable captures are `quarantined`. The live pointer never moves backwards.
- **Freshness.** Each response computes `fresh` (≤90 s), `stale`, or `unavailable` from source time, including on warm cache hits. When predictions are missing or not fresh, the API returns scheduled departures from the newest imported GTFS static version, using the GTFS noon-minus-12h service-day rule (DST-safe, times past 24:00 supported).
- **Replay.** `transit replay <name> <captures...>` reprocesses captures into the namespace `replay-<name>` with a pinned clock and prints a fingerprint of the published observations. The same inputs give the same fingerprint, and replay refuses the `live` namespace.
- **Schedule import.** Bulk `COPY` inside one transaction, keyed by the zip's SHA-256, so re-imports are no-ops. The real METRO feed (235,038 stop times) imports in about 2.5 s locally.

API contract: `api/openapi.yaml`. A test checks that the served routes match the spec.

## Run locally

```
docker compose up -d db                    # or any PostgreSQL 17
export DATABASE_URL=postgres://postgres:local-dev-only@127.0.0.1:5432/transit?sslmode=disable
go build -o transit ./cmd/transit
./transit schedule fixtures/metro-static.zip
SCHEDULE_VERSION=<printed id> ./transit ingest fixtures/metro-trips.pb
./transit replay check fixtures/metro-trips.pb
WORKERS=2 RESPONSE_CACHE=1 ./transit       # POLL_URL / POLL_INTERVAL (>=30s) enable polling
cd web && npm ci && npm run dev            # operator page, proxies /api to :8080
```

Environment: `DATABASE_URL`, `LISTEN_ADDR`, `WORKERS`, `RESPONSE_CACHE`, `ADMIN_TOKEN`, `ALLOWED_ORIGIN`, `POLL_URL`, `POLL_INTERVAL`, `SCHEDULE_VERSION`.

## Tests

```
export TEST_DATABASE_URL=postgres://postgres:local-dev-only@127.0.0.1:5432/transit_test?sslmode=disable
gofmt -l cmd internal; go vet ./...
go test -race ./...
go test ./internal/transit -run '^$' -fuzz FuzzDecode -fuzztime 30s
go test ./internal/transit -run '^$' -fuzz FuzzParseTime -fuzztime 30s
cd web && npm test && npm run build && npm run e2e
```

Database tests use a real PostgreSQL and skip when `TEST_DATABASE_URL` is unset. Raw outputs from local runs are in `docs/evidence/`.

## Benchmark

Exploratory same-machine k6 comparison of uncached, indexed and cached departures over 1M synthetic observations: [bench/PROTOCOL.md](bench/PROTOCOL.md) and [bench/REPORT.md](bench/REPORT.md). Without the stop index the API saturates at 100 requests/s; with it, every run held 100 requests/s with 0 errors and departures p95 of 2.6–2.7 ms.

## Scope

Single agency, local development only. Not deployed, so there is no measured uptime, cost or traffic. Fixtures are one static zip and one trip-updates snapshot, captured once with no repeated polling of the upstream feed.
