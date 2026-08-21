#!/usr/bin/env bash
set -Eeuo pipefail

# Logical backup plus object-storage upload. Continuous WAL/PITR is provided by
# the managed PostgreSQL service (see docs/public-beta-operations.md); this
# script verifies the logical recovery point and archives it separately.
root=${1:-/opt/relaydock}
cd "$root"
set -a
. ./.env
set +a
umask 077

: "${DATABASE_URL:?DATABASE_URL must be set}"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
destination="data/backups/modeldock-${timestamp}.dump"
mkdir -p data/backups

if command -v pg_dump >/dev/null 2>&1; then
  pg_dump --format=custom --no-owner --no-privileges "$DATABASE_URL" >"$destination"
else
  docker run --rm --network host -e DATABASE_URL="$DATABASE_URL" postgres:17-alpine \
    sh -ec 'pg_dump --format=custom --no-owner --no-privileges "$DATABASE_URL"' >"$destination"
fi
test -s "$destination"
sha256sum "$destination" >"${destination}.sha256"

if [[ -n "${BACKUP_OBJECT_URI:-}" ]]; then
  case "$BACKUP_OBJECT_URI" in
    s3://*)
      command -v aws >/dev/null 2>&1 || { echo "aws CLI is required for BACKUP_OBJECT_URI" >&2; exit 1; }
      if [[ -n "${BACKUP_SSE_KMS_KEY_ID:-}" ]]; then
        aws s3 cp "$destination" "$BACKUP_OBJECT_URI/$(basename "$destination")" --sse aws:kms --sse-kms-key-id "$BACKUP_SSE_KMS_KEY_ID" --only-show-errors
      else
        aws s3 cp "$destination" "$BACKUP_OBJECT_URI/$(basename "$destination")" --sse AES256 --only-show-errors
      fi
      ;;
    *) echo "BACKUP_OBJECT_URI must use s3://" >&2; exit 1 ;;
  esac
fi

find data/backups -type f -mtime +14 -delete
echo "Created and verified $destination"
