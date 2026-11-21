#!/usr/bin/env bash
# Runs on a load instance in the host's VPC. Same warmup, k6 script and file names as run-load.ps1.
set -euo pipefail
host=${1:?host private address}
key=${2:?key allowed to run bench-host.sh}
out=${3:-bench/raw/v3}
rate=${RATE:-100}
duration=${DURATION:-600s}
warmup=${WARMUP:-30s}
repeats=${REPEATS:-3}
cd "$(dirname "$0")/../.."
mkdir -p "$out"
base="http://$host:18080"
orders=("indexed cached" "cached indexed" "indexed cached")

stamp() { date -u +%FT%TZ; }
on_host() { ssh -i "$key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "ec2-user@$host" "$@"; }
trap 'on_host stop >/dev/null 2>&1 || true' EXIT

on_host env > "$out/host-environment.txt"
k6 version > "$out/k6-version.txt"
git rev-parse HEAD > "$out/load-git-sha.txt"
{
  uname -a
  nproc
  free -m | head -2
  token=$(curl -s -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token || true)
  for k in instance-type placement/availability-zone local-ipv4; do
    printf '%s: %s\n' "$k" "$(curl -s -H "X-aws-ec2-metadata-token: $token" "http://169.254.169.254/latest/meta-data/$k")"
  done
} > "$out/load-environment.txt"

for ((rep = 0; rep < repeats; rep++)); do
  for v in ${orders[$((rep % 3))]}; do
    on_host "variant $rep $v" > "$out/plan-$rep-$v.txt"
    if [ ! -s "$out/network-baseline.txt" ]; then
      for _ in $(seq 200); do curl -s -o /dev/null -w '%{time_connect} %{time_total}\n' "$base/readyz"; done > "$out/network-baseline.txt"
    fi
    k6 run --quiet -e BASE_URL="$base" -e RATE="$rate" -e DURATION="$warmup" --summary-export "$out/warmup-$rep-$v.json" bench/k6.js > "$out/warmup-$rep-$v.txt" 2>&1 || true
    stamp > "$out/started-$rep-$v.txt"
    k6 run -e BASE_URL="$base" -e RATE="$rate" -e DURATION="$duration" --summary-export "$out/k6-$rep-$v.json" bench/k6.js > "$out/k6-$rep-$v.txt" 2>&1 || echo "k6 exit $?" >> "$out/k6-$rep-$v.txt"
    stamp > "$out/finished-$rep-$v.txt"
    echo "$(stamp) rep $rep $v done" | tee -a "$out/progress.txt"
  done
done
