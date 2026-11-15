#!/usr/bin/env bash
# Local deploy/rollback/backup/restore drill in an isolated compose project.
set -euo pipefail
cd "$(dirname "$0")/.."
. ops/lib.sh
old=${1:?usage: drill.sh <old image tag> <new image tag>}
new=${2:?usage: drill.sh <old image tag> <new image tag>}
export COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-transit-drill} API_PORT=${API_PORT:-18181}
export DEPLOY_STATE=${DEPLOY_STATE:-.deploy/drill} BACKUP_DIR=${BACKUP_DIR:-backups/drill}
api=http://127.0.0.1:$API_PORT
ts() { date -u +%FT%TZ; }
ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
run() { docker run --rm --network "${COMPOSE_PROJECT_NAME}_default" -v "$PWD/fixtures:/fixtures:ro" -e DATABASE_URL="postgres://postgres:${POSTGRES_PASSWORD:-local-dev-only}@db/${DB:-transit}?sslmode=disable" -e SCHEDULE_VERSION="${SCHEDULE_VERSION:-}" "classmap-transit:$old" "$@"; }

echo "$(ts) drill start old=$old new=$new"
rm -rf "$DEPLOY_STATE"
ops/deploy.sh "$old"
schedule=$(run schedule /fixtures/metro-static.zip)
echo "schedule $schedule"
SCHEDULE_VERSION=$schedule run ingest /fixtures/metro-trips.pb
for _ in $(seq 1 30); do
  status=$(curl -sf "$api/v1/feed-status")
  case $status in *'"generation":'[0-9]*) break ;; esac
  sleep 1
done
echo "feed-status $status"
curl -sf "$api/v1/ops"; echo

t=$(ms); ops/deploy.sh "$new"; echo "deploy-new-ms $(( $(ms) - t ))"
curl -sf "$api/v1/feed-status"; echo
t=$(ms); ops/rollback.sh; echo "rollback-ms $(( $(ms) - t ))"
[ "$(cat "$DEPLOY_STATE/current")" = "$old" ] || { echo "rollback did not restore $old" >&2; exit 1; }
after=$(curl -sf "$api/v1/feed-status"); echo "feed-status-after-rollback $after"
[ "$after" = "$status" ] || echo "feed-status changed across rollback (freshness clock advances)"

mkdir -p "$BACKUP_DIR"
ops/reconcile.sh transit > "$BACKUP_DIR.source.txt"
t=$(ms); dump=$(ops/backup.sh); echo "backup-ms $(( $(ms) - t )) $dump $(wc -c < "$dump") bytes"
dc exec -T db dropdb -U postgres --if-exists transit_restore
t=$(ms); ops/restore.sh "$dump" transit_restore; echo "restore-ms $(( $(ms) - t ))"
ops/reconcile.sh transit_restore > "$BACKUP_DIR.restored.txt"
if diff "$BACKUP_DIR.source.txt" "$BACKUP_DIR.restored.txt"; then echo "reconcile identical"; else echo "reconcile DIFFERS" >&2; exit 1; fi
cat "$BACKUP_DIR.restored.txt"
docker run -d --rm --name transit-drill-restored --network "${COMPOSE_PROJECT_NAME}_default" -p "127.0.0.1:18182:8080" -e LISTEN_ADDR=0.0.0.0:8080 -e SKIP_MIGRATE=1 \
  -e DATABASE_URL="postgres://postgres:${POSTGRES_PASSWORD:-local-dev-only}@db/transit_restore?sslmode=disable" "classmap-transit:$new" >/dev/null
for _ in $(seq 1 30); do curl -sf http://127.0.0.1:18182/readyz >/dev/null && break; sleep 1; done
echo "restored-api-feed-status $(curl -sf http://127.0.0.1:18182/v1/feed-status)"
docker stop transit-drill-restored >/dev/null
echo "$(ts) drill done"
