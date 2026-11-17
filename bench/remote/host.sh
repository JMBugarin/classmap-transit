#!/usr/bin/env bash
# Runs on the deployed host. Usable as an SSH forced command: arguments come from SSH_ORIGINAL_COMMAND.
set -euo pipefail
dir=/opt/classmap-transit
cd "$dir"
set -a; . ./.env; set +a
tag=${BENCH_TAG:-dffd820}
net=classmap-transit_default
db=classmap-transit-db-1
api=transit-bench-api
results=$dir/bench-results
url="postgres://postgres:${POSTGRES_PASSWORD:-local-dev-only}@db/transit_bench?sslmode=disable"
read -r -a args <<< "${SSH_ORIGINAL_COMMAND:-$*}"

sql() { docker exec -i "$db" psql -U postgres -d transit_bench -qAt -v ON_ERROR_STOP=1 -c "$1"; }

stop_all() {
  if [ -f /tmp/bench-heartbeat.pid ]; then kill "$(cat /tmp/bench-heartbeat.pid)" 2>/dev/null || true; rm -f /tmp/bench-heartbeat.pid; fi
  docker rm -f "$api" >/dev/null 2>&1 || true
}

case "${args[0]:-}" in
  prepare)
    docker exec "$db" psql -U postgres -qAt -c "SELECT 1 FROM pg_database WHERE datname='transit_bench'" | grep -q 1 ||
      docker exec "$db" createdb -U postgres transit_bench
    docker run --rm --network "$net" -e DATABASE_URL="$url" -e BENCH_ROWS=1000000 -e BENCH_SEED=20261115 "classmap-transit:$tag" bench-seed
    sql "ANALYZE observations; SELECT count(*), count(DISTINCT stop) FROM observations"
    ;;
  variant)
    rep=${args[1]:?rep}; v=${args[2]:?variant}
    [[ $rep =~ ^[0-9]$ ]] || { echo "bad rep" >&2; exit 2; }
    stop_all
    case $v in
      uncached) sql "DROP INDEX IF EXISTS observations_stop"; cache=0 ;;
      indexed) sql "CREATE INDEX IF NOT EXISTS observations_stop ON observations(stop,predicted,generation)"; cache=0 ;;
      cached) sql "CREATE INDEX IF NOT EXISTS observations_stop ON observations(stop,predicted,generation)"; cache=1 ;;
      *) echo "bad variant" >&2; exit 2 ;;
    esac
    sql "ANALYZE observations"
    docker run -d --name "$api" --network "$net" -p 0.0.0.0:18080:8080 -e DATABASE_URL="$url" -e LISTEN_ADDR=0.0.0.0:8080 \
      -e WORKERS=1 -e RESPONSE_CACHE=$cache -e SKIP_MIGRATE=1 "classmap-transit:$tag" >/dev/null
    for _ in $(seq 150); do curl -sf http://127.0.0.1:18080/readyz >/dev/null && break; sleep 0.2; done
    curl -sf http://127.0.0.1:18080/readyz >/dev/null
    nohup bash -c "while true; do docker exec $db psql -U postgres -d transit_bench -qAt -c \"UPDATE feed_state SET source_time=extract(epoch FROM now())::bigint WHERE ns='live'\" >/dev/null; sleep 15; done" >/dev/null 2>&1 &
    echo $! > /tmp/bench-heartbeat.pid
    sql "UPDATE feed_state SET source_time=extract(epoch FROM now())::bigint WHERE ns='live'"
    echo "# $(date -u +%FT%TZ) rep $rep $v cache=$cache image=$tag"
    sql "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT trip,stop,predicted,event_time FROM observations WHERE generation=(SELECT generation FROM feed_state WHERE ns='live') AND stop='S0000' ORDER BY predicted,trip LIMIT 20"
    ;;
  stop)
    stop_all
    echo "stopped"
    ;;
  env)
    uname -a; nproc; free -m | head -2; docker --version; docker image inspect "classmap-transit:$tag" --format '{{.Id}}'
    docker exec "$db" psql -U postgres -qAt -c 'SELECT version()'
    ;;
  upload)
    name=${args[1]:?name}
    [[ $name =~ ^[A-Za-z0-9._-]+$ ]] || { echo "bad name" >&2; exit 2; }
    mkdir -p "$results"
    head -c 52428800 > "$results/$name.tar"
    sha256sum "$results/$name.tar"
    ;;
  *)
    echo "usage: prepare | variant <rep> <uncached|indexed|cached> | stop | env | upload <name>" >&2
    exit 2
    ;;
esac
