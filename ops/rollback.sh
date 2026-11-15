#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
state=${DEPLOY_STATE:-.deploy}
prev=$(cat "$state/previous")
exec ops/deploy.sh "$prev"
