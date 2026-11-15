#!/usr/bin/env bash
# Exploratory local comparison. API, PostgreSQL (Colima VM) and k6 share one machine.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${BENCH_DATABASE_URL:?set BENCH_DATABASE_URL to a *_bench database}"
OUT=${OUT:-bench/raw/$(date -u +%Y%m%dT%H%M%SZ)}
RATE=${RATE:-100} DURATION=${DURATION:-600s} WARMUP=${WARMUP:-30s} REPEATS=${REPEATS:-3}
PORT=18080
mkdir -p "$OUT"
go build -o "$OUT/transit" ./cmd/transit
DATABASE_URL=$BENCH_DATABASE_URL "$OUT/transit" bench-seed > "$OUT/seed.txt"
psql "$BENCH_DATABASE_URL" -Atc "ANALYZE observations; SELECT count(*), count(DISTINCT stop) FROM observations" > "$OUT/rows.txt"
{ uname -a; sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize 2>/dev/null || true; go version; k6 version; psql "$BENCH_DATABASE_URL" -Atc 'SELECT version()'; git rev-parse HEAD; git status --porcelain; } > "$OUT/environment.txt"

heartbeat() { while true; do psql "$BENCH_DATABASE_URL" -qAtc "UPDATE feed_state SET source_time=extract(epoch FROM now())::bigint WHERE ns='live'" >/dev/null; sleep 15; done; }
heartbeat & HB=$!
trap 'kill $HB 2>/dev/null || true; [ -n "${API:-}" ] && kill $API 2>/dev/null || true' EXIT

plan() {
  psql "$BENCH_DATABASE_URL" -Atc "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT trip,stop,predicted,event_time FROM observations WHERE generation=(SELECT generation FROM feed_state WHERE ns='live') AND stop='S0000' ORDER BY predicted,trip LIMIT 20" > "$1"
}

variant() {
  case $1 in
    uncached) psql "$BENCH_DATABASE_URL" -qc "DROP INDEX IF EXISTS observations_stop"; CACHE=0 ;;
    indexed) psql "$BENCH_DATABASE_URL" -qc "CREATE INDEX IF NOT EXISTS observations_stop ON observations(stop,predicted,generation)"; CACHE=0 ;;
    cached) psql "$BENCH_DATABASE_URL" -qc "CREATE INDEX IF NOT EXISTS observations_stop ON observations(stop,predicted,generation)"; CACHE=1 ;;
  esac
}

ORDERS=("uncached indexed cached" "indexed cached uncached" "cached uncached indexed")
for rep in $(seq 0 $((REPEATS - 1))); do
  for v in ${ORDERS[$((rep % 3))]}; do
    variant "$v"
    psql "$BENCH_DATABASE_URL" -qAtc "ANALYZE observations"
    DATABASE_URL=$BENCH_DATABASE_URL LISTEN_ADDR=127.0.0.1:$PORT WORKERS=1 RESPONSE_CACHE=$CACHE SKIP_MIGRATE=1 "$OUT/transit" > "$OUT/api-$rep-$v.log" 2>&1 &
    API=$!
    until curl -sf "http://127.0.0.1:$PORT/readyz" >/dev/null; do sleep 0.2; done
    plan "$OUT/plan-$rep-$v.txt"
    k6 run --quiet -e RATE=$RATE -e DURATION=$WARMUP --summary-export "$OUT/warmup-$rep-$v.json" bench/k6.js > "$OUT/warmup-$rep-$v.txt" 2>&1 || true
    date -u +%FT%TZ > "$OUT/started-$rep-$v.txt"
    k6 run -e RATE=$RATE -e DURATION=$DURATION --summary-export "$OUT/k6-$rep-$v.json" bench/k6.js > "$OUT/k6-$rep-$v.txt" 2>&1 || echo "k6 exit $?" >> "$OUT/k6-$rep-$v.txt"
    kill $API; wait $API 2>/dev/null || true
    API=
    echo "$(date -u +%FT%TZ) rep $rep $v done" | tee -a "$OUT/progress.txt"
  done
done
