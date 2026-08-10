#!/usr/bin/env bash
set -Eeuo pipefail

root=${1:-/opt/relaydock}
cd "$root"
set -a
. ./.env
set +a
umask 077

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
destination="data/backups/relaydock-${timestamp}.sql.gz"
mkdir -p data/backups
docker compose --env-file .env exec -T postgres \
  pg_dump --format=plain --no-owner --no-privileges -U "$POSTGRES_USER" "$POSTGRES_DB" \
  | gzip -9 >"$destination"
test -s "$destination"
sha256sum "$destination" >"${destination}.sha256"
find data/backups -type f -mtime +14 -delete
echo "Created $destination"
