#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
. ops/lib.sh
dump=${1:?usage: restore.sh <dump> <new database name>}
target=${2:?usage: restore.sh <dump> <new database name>}
[[ $target =~ ^[a-z_][a-z0-9_]*$ ]] || { echo "invalid database name" >&2; exit 1; }
(cd "$(dirname "$dump")" && sha256 -c "$(basename "$dump").sha256")
dc exec -T db createdb -U postgres "$target"
dc exec -T db pg_restore -U postgres --no-owner --exit-on-error --single-transaction -d "$target" < "$dump"
