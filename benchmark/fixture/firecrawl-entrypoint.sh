#!/usr/bin/env bash
set -euo pipefail

pids=()
cleanup() {
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait || true
}
trap cleanup EXIT
trap 'exit 143' TERM INT

install -d -o postgres -g postgres /tmp/huntsman-bench-pg
runuser -u postgres -- /usr/lib/postgresql/15/bin/initdb -D /tmp/huntsman-bench-pg --auth=trust >/tmp/pg-init.log
runuser -u postgres -- /usr/lib/postgresql/15/bin/postgres -D /tmp/huntsman-bench-pg \
  -c listen_addresses=127.0.0.1 -c shared_preload_libraries=pg_cron \
  -c cron.database_name=postgres -c shared_buffers=128MB -c max_connections=2000 > /tmp/postgres.log 2>&1 &
pids+=("$!")
redis-server --bind 127.0.0.1 --save "" --appendonly no > /tmp/redis.log 2>&1 &
pids+=("$!")
rabbitmq-server > /tmp/rabbitmq.log 2>&1 &
pids+=("$!")

ready=false
for ((i=0; i<120; i++)); do
  if pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
if [[ "$ready" != true ]]; then cat /tmp/postgres.log; exit 1; fi
psql -h 127.0.0.1 -U postgres -v ON_ERROR_STOP=1 -f /opt/nuq.sql >/tmp/pg-schema.log 2>&1
# The upstream schema reserves several GB of WAL; keep its SQL, but size storage for this one-core test.
psql -h 127.0.0.1 -U postgres -v ON_ERROR_STOP=1 -c "ALTER SYSTEM SET min_wal_size='80MB'" \
  -c "ALTER SYSTEM SET max_wal_size='1GB'" -c "SELECT pg_reload_conf()" >/dev/null
ready=false
for ((i=0; i<120; i++)); do
  if (echo >/dev/tcp/127.0.0.1/5672) 2>/dev/null; then ready=true; break; fi
  sleep 1
done
if [[ "$ready" != true ]]; then cat /tmp/rabbitmq.log; exit 1; fi

(cd /opt/playwright && PORT=3000 node dist/api.js) > /tmp/playwright.log 2>&1 &
pids+=("$!")
if [[ "${FIRECRAWL_SCRAPE_ONLY:-}" == 1 ]]; then
  # /v1/scrape runs inside the API process; the harness's queue, extract and NuQ workers sit idle.
  export NUQ_DATABASE_URL="postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}/${POSTGRES_DB}"
  export NUQ_DATABASE_URL_LISTEN="$NUQ_DATABASE_URL" NUQ_POD_NAME=api
  (cd /app && node dist/src/index.js) &
else
  (cd /app && node dist/src/harness.js --start-docker) &
fi
pids+=("$!")
wait -n "${pids[@]}"
echo "A Firecrawl stack process exited" >&2
exit 1
