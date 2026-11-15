#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
dump=${1:?usage: restore.sh <dump> <new database name>}
target=${2:?usage: restore.sh <dump> <new database name>}
[[ $target =~ ^[a-z_][a-z0-9_]*$ ]] || { echo "invalid database name" >&2; exit 1; }
shasum -a 256 -c "$dump.sha256"
docker compose exec -T db createdb -U postgres "$target"
docker compose exec -T db pg_restore -U postgres --no-owner --exit-on-error --single-transaction -d "$target" < "$dump"
