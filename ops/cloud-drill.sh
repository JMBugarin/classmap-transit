#!/usr/bin/env bash
# Run from the operator machine against a deployed host. Destroys the host's database volume.
set -euo pipefail
cd "$(dirname "$0")/.."
host=${1:?usage: cloud-drill.sh <user@host> <old tag> <new tag>}
old=${2:?usage: cloud-drill.sh <user@host> <old tag> <new tag>}
new=${3:?usage: cloud-drill.sh <user@host> <old tag> <new tag>}
wait=${LOSS_WAIT:-300}
dir=${REMOTE_DIR:-/opt/classmap-transit}
local_backups=${LOCAL_BACKUPS:-backups/cloud}
r() { ssh -o BatchMode=yes "$host" "cd $dir && $*"; }
sql() { r "docker compose exec -T db psql -U postgres -d transit -At -F ' ' -v ON_ERROR_STOP=1"; }
now() { r 'date +%s%3N'; }
ts() { date -u +%FT%TZ; }
status() { r 'curl -sf http://127.0.0.1:8080/v1/feed-status'; }
gen() { status | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d.get("generation") or 0)'; }
mkdir -p "$local_backups"

echo "$(ts) cloud drill host=$host old=$old new=$new loss_wait=${wait}s"
echo "start-tag $(r 'cat .deploy/current') generation $(gen)"

t=$(now); r "ops/deploy.sh $new" >/dev/null; echo "deploy-new-ms $(( $(now) - t )) generation $(gen)"
t=$(now); r "ops/deploy.sh $old" >/dev/null; echo "deploy-old-ms $(( $(now) - t )) generation $(gen)"
t=$(now); r ops/rollback.sh >/dev/null; echo "rollback-to-$(r 'cat .deploy/current')-ms $(( $(now) - t )) generation $(gen)"
[ "$(r 'cat .deploy/current')" = "$new" ] || { echo "rollback did not restore $new" >&2; exit 1; }

r ops/reconcile.sh transit > "$local_backups/source.txt"
t=$(now); dump=$(r ops/backup.sh); echo "backup-ms $(( $(now) - t )) $dump $(r "wc -c < $dump") bytes"
backup_at=$(sql <<< "SELECT extract(epoch FROM now())::bigint")
backup_gen=$(gen)
t=$(now); scp -q -o BatchMode=yes "$host:$dir/$dump" "$host:$dir/$dump.sha256" "$local_backups/"; echo "offhost-copy-ms $(( $(now) - t ))"
name=$(basename "$dump")
(cd "$local_backups" && sha256sum -c "$name.sha256" 2>/dev/null || shasum -a 256 -c "$name.sha256")

echo "$(ts) polling for ${wait}s after backup before failure"
sleep "$wait"
lost=$(sql <<< "SELECT count(*) FILTER (WHERE fetched_at > to_timestamp($backup_at)), (SELECT count(*) FROM fetches WHERE fetched_at > to_timestamp($backup_at)), extract(epoch FROM now())::bigint - $backup_at FROM captures")
echo "at-failure captures/fetches-after-backup/seconds-since-backup $lost"
echo "at-failure generation $(gen) (backup generation $backup_gen)"

fail=$(now); echo "$(ts) failure: docker compose down -v (database volume destroyed)"
r 'docker compose down -v' >/dev/null 2>&1
r 'rm -rf backups && mkdir -p backups'
t=$(now); scp -q -o BatchMode=yes "$local_backups/$name" "$local_backups/$name.sha256" "$host:$dir/backups/"; echo "copy-back-ms $(( $(now) - t ))"
t=$(now); r 'docker compose up -d --wait db' >/dev/null 2>&1; echo "db-up-ms $(( $(now) - t ))"
r 'docker compose exec -T db dropdb -U postgres transit'
t=$(now); r "ops/restore.sh backups/$name transit" >/dev/null; echo "restore-ms $(( $(now) - t ))"
r ops/reconcile.sh transit > "$local_backups/restored.txt"
if diff "$local_backups/source.txt" "$local_backups/restored.txt"; then echo "reconcile identical to backup-time report"; else echo "reconcile DIFFERS" >&2; exit 1; fi
r "ops/deploy.sh $new" >/dev/null; ready=$(now); echo "failure-to-ready-ms $(( ready - fail )) generation $(gen)"
for _ in $(seq 1 60); do [ "$(gen)" -gt "$backup_gen" ] && break; sleep 5; done
echo "failure-to-new-generation-ms $(( $(now) - fail )) generation $(gen)"
status; echo
echo "$(ts) cloud drill done"
