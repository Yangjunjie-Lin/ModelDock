#!/usr/bin/env bash
set -Eeuo pipefail

backup=${1:?usage: restore-drill.sh path/to/modeldock.dump[.gz]}
backup=$(realpath "$backup")
[[ -f "$backup" ]] || { echo "backup file does not exist" >&2; exit 1; }
case "$backup" in
  *.dump|*.dump.gz|*.sql.gz) ;;
  *) echo "refusing an unrecognised backup extension" >&2; exit 1 ;;
esac

run_id="$(date -u +%Y%m%d%H%M%S)-$$"
container="modeldock-restore-drill-${run_id}"
password="$(od -An -N24 -tx1 /dev/urandom | tr -d '[:space:]')"
cleanup() { docker rm --force "$container" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run --detach --name "$container" -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=restore \
  --network none postgres:17-alpine >/dev/null
for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U postgres -d restore >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$container" pg_isready -U postgres -d restore >/dev/null

case "$backup" in
  *.sql.gz) gzip -dc "$backup" | docker exec -i "$container" psql -U postgres -d restore -v ON_ERROR_STOP=1 >/dev/null ;;
  *.dump.gz) gzip -dc "$backup" | docker exec -i "$container" pg_restore -U postgres -d restore --no-owner --exit-on-error ;;
  *.dump) docker exec -i "$container" pg_restore -U postgres -d restore --no-owner --exit-on-error <"$backup" ;;
esac

docker exec "$container" psql -U postgres -d restore -Atqc \
  "SELECT 'schema_migrations='||count(*) FROM schema_migrations; SELECT 'audit_hash_columns='||count(*) FROM information_schema.columns WHERE table_name='audit_logs' AND column_name IN ('entry_hash','previous_hash');"
echo "Restore drill passed in isolated container $container"
