#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
. ops/lib.sh
db=${DB_NAME:-transit}
dir=${BACKUP_DIR:-backups}
mkdir -p "$dir"
f="$dir/$db-$(date -u +%Y%m%dT%H%M%SZ).dump"
dc exec -T db pg_dump -U postgres --format=custom --no-owner "$db" > "$f"
shasum -a 256 "$f" > "$f.sha256"
echo "$f"
