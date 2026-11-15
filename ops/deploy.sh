#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
tag=${1:?usage: deploy.sh <image tag>}
state=${DEPLOY_STATE:-.deploy}
mkdir -p "$state"
docker image inspect "classmap-transit:$tag" >/dev/null
prev=$(cat "$state/current" 2>/dev/null || true)
TAG=$tag docker compose up -d --no-build api
for _ in $(seq 1 60); do
  if curl -sf http://127.0.0.1:${API_PORT:-8080}/readyz >/dev/null; then
    [ -n "$prev" ] && [ "$prev" != "$tag" ] && echo "$prev" > "$state/previous"
    echo "$tag" > "$state/current"
    echo "deployed $tag"
    exit 0
  fi
  sleep 1
done
echo "readiness failed for $tag" >&2
if [ -n "$prev" ]; then TAG=$prev docker compose up -d --no-build api; echo "restored $prev" >&2; fi
exit 1
