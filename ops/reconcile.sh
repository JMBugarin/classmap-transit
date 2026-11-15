#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
. ops/lib.sh
dc exec -T db psql -U postgres -d "${1:?usage: reconcile.sh <database>}" -At -F ' ' -v ON_ERROR_STOP=1 -f - < ops/reconcile.sql
