#!/usr/bin/env bash
# Usage: benchmark/run.sh <name> <container> <scrape-url-inside-network> <fixture-url>
set -uo pipefail
name=$1 container=$2 target=$3 fixture=$4
out="$(cd "$(dirname "$0")/.." && pwd)/results/$name"
mkdir -p "$out"
summary="$out/summary.txt"
: > "$summary"

peak_mib() {
  local peak=0 value
  while kill -0 "$1" 2>/dev/null; do
    value=$(docker stats --no-stream --format '{{.MemUsage}}' "$container" | awk '{print $1}')
    value=$(awk -v v="$value" 'BEGIN{n=v+0; if (v ~ /GiB/) n*=1024; else if (v ~ /KiB/) n/=1024; printf "%d", n}')
    (( value > peak )) && peak=$value
  done
  echo "$peak"
}

bench() {
  local label=$1; shift
  docker run --rm --network hm-bench -v "$out:/out" --entrypoint bench huntsman-local:dev \
    -target "$target" -fixture "$fixture" -output "/out/$label.json" "$@" > "$out/$label.log" 2>&1 &
  local pid=$!
  local peak; peak=$(peak_mib "$pid")
  wait "$pid"
  echo "$label $(head -1 "$out/$label.log") peak_mem=${peak}MiB" | tee -a "$summary"
}

for c in 1 4 16 64; do
  n=$(( c < 16 ? 32 : 128 ))
  for kind in static js; do
    bench "cold-$kind-c$c" -kind "$kind" -mode cold -concurrency "$c" -requests "$n"
  done
done
bench cold-delayed-c16 -kind delayed -mode cold -concurrency 16 -requests 128
bench cold-delayed-wait500-c16 -kind delayed -mode cold -concurrency 16 -requests 128 -wait-ms 500
bench warm-js-c64 -kind js -mode warm -concurrency 64 -requests 256
bench duplicate-js-c64 -kind js -mode duplicate -concurrency 64 -requests 256
