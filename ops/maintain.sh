#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
. ops/lib.sh
tag=$(cat "${DEPLOY_STATE:-.deploy}/current")
ops/backup.sh
TAG=$tag dc run --rm --no-deps -e RETAIN="${RETAIN:-168h}" api prune
find "${BACKUP_DIR:-backups}" -name '*.dump*' -mtime +"${KEEP_DAYS:-7}" -delete
