#!/usr/bin/env bash
# Full dataset build after `01_candidates.py cc`. Every step resumes where it stopped, so re-running is safe.
# FROM=<step name> skips every step before it (e.g. FROM=label ./run.sh).
# Needs: local Firecrawl on :3002 (gap-fill search) and a logged-in `copilot` CLI (labelling).
# The 24h stability check, human review and export run separately (see SPEC.md §3).
set -euo pipefail
cd "$(dirname "$0")"
PY="uv run python"
FETCH_CONC=${FETCH_CONC:-40}
PRE=${PRE:-75000}       # pages to label (>=150 words); preselect-2 labels every page with enough text
MAX_OK=${MAX_OK:-62000} # stop the bulk crawl here
FROM=${FROM:-}

# run <name> <command...>: log a header and run, unless we are still before $FROM
run() {
  local name=$1; shift
  if [ -n "$FROM" ]; then
    [ "$name" != "$FROM" ] && { echo "skip $name"; return; }
    FROM=""
  fi
  echo; echo "=== $(date '+%H:%M:%S') $name"
  "$@"
}
utc_now() { date -u '+%Y-%m-%d %H:%M:%S'; }

run crawl        $PY 02_fetch.py --concurrency "$FETCH_CONC" --max-ok "$MAX_OK"
run tag          $PY 03_tag.py

# Before spending LLM calls, fill buckets the crawl sample cannot (rare languages, anti-bot, paywalls...).
run preselect    $PY 06_select.py --pre "$PRE"
run search       $PY 01_candidates.py search --gaps gaps_pre.json
run fetch-search $PY 02_fetch.py --concurrency "$FETCH_CONC" --source search
run tag-search   $PY 03_tag.py
run preselect-2  $PY 06_select.py --pre "$PRE" --pre-all  # label every page with enough text

run label        $PY 04_label.py
run verify       $PY 05_validate.py verify
run select       $PY 06_select.py

# gap-fill search needs local Firecrawl; its containers may have been stopped to free RAM during fetching/labelling
firecrawl_up() {
  docker start $(docker ps -aq --filter name=firecrawl-) >/dev/null
  for _ in $(seq 60); do
    curl -s -m 5 -o /dev/null -X POST localhost:3002/v1/search -H 'Content-Type: application/json' -d '{"query":"x","limit":1}' && return
    sleep 5
  done
  echo "local Firecrawl did not come up on :3002" >&2; return 1
}

for round in 1 2 3; do
  [ -n "$FROM" ] && break
  firecrawl_up
  short=$(python3 -c "import json;print(sum(g['need']-g['have'] for g in json.load(open('../out/gaps.json'))))")
  echo; echo "=== $(date '+%H:%M:%S') gap-fill round $round (total shortfall $short)"
  [ "$short" -lt 50 ] && break
  since=$(utc_now)
  $PY 01_candidates.py search
  $PY 02_fetch.py --concurrency "$FETCH_CONC" --source search --added-since "$since"
  $PY 03_tag.py
  $PY 04_label.py --source search --added-since "$since"
  $PY 05_validate.py verify
  $PY 06_select.py
done

run review-llm   $PY 07_review.py llm
run reselect     $PY 06_select.py
run review-human sh -c "$PY 07_review.py sample && $PY 07_review.py html"
echo; echo "=== $(date '+%H:%M:%S') done. Next: 05_validate.py stability (>=24h after fetch), review/index.html, 07_review.py apply, 06_select.py, 08_export.py"
