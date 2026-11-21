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

k6 comparison of uncached, indexed and cached departures over 1M synthetic observations, at 100 requests/s for 10 minutes per run.

- **v3, deployed host, load from the same Availability Zone** ([protocol](bench/PROTOCOL-v3.md), [report](bench/REPORT-v3.md)). With the stop index, every run held 100 requests/s with 0 errors and departures p95 of 1.64–1.65 ms, or 1.46–1.50 ms with the response cache. A TCP connect inside the zone takes about 0.3 ms, so almost all of this is the service itself.
- **v2, deployed host, load from a separate machine** ([protocol](bench/PROTOCOL-v2.md), [report](bench/REPORT-v2.md)). With the stop index, every run held 100 requests/s with 0 errors and departures p95 of 36.2–36.6 ms. The query plan ran in about 0.3 ms on the host, so nearly all of that is the home-to-us-west-2 network path (v3 measured 1.65 ms from inside the zone). Without the index, 70% of requests hit the 5 s statement timeout.
- **v1, one laptop, exploratory** ([protocol](bench/PROTOCOL.md), [report](bench/REPORT.md)). With the index, departures p95 was 2.57–2.72 ms over loopback. Without it, the API saturated at 100 requests/s.

## Operations

Image-tagged deploys, rollback, retention, backup/restore and the cost estimate are in [docs/operations.md](docs/operations.md).

## Scope

Single agency. Deployed on one EC2 t4g.small since 2026-11-17, with a measured restore drill in docs/operations.md. Since 2026-11-18 a Caddy ingress with automatic TLS serves the routes the Classmap web map needs at `https://32-188-109-110.sslip.io`: departures, feed status, vehicles and alerts, plus the opt-in activity and room-feedback posts. Everything else, including admin, ops and observations, returns 404 there and is reachable only over SSH. No measured uptime, cost or traffic yet. Fixtures are one static zip and one trip-updates snapshot, captured once with no repeated polling of the upstream feed.
