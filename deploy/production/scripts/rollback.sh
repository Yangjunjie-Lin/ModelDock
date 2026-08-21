#!/usr/bin/env bash
set -Eeuo pipefail

root=${1:-/opt/relaydock}
tag=${2:?usage: rollback.sh /opt/relaydock <previous-immutable-tag>}
[[ "$tag" =~ ^[A-Za-z0-9_.:@/-]+$ ]] || { echo "invalid image tag" >&2; exit 1; }
cd "$root"
docker compose --env-file .env config --quiet
RELAYDOCK_IMAGE_TAG="$tag" docker compose --env-file .env pull relaydock
RELAYDOCK_IMAGE_TAG="$tag" docker compose --env-file .env up -d --no-deps --scale relaydock="${RELAYDOCK_REPLICAS:-2}" relaydock
for _ in {1..60}; do
  if docker compose --env-file .env exec -T relaydock wget -qO- http://127.0.0.1:8080/readyz >/dev/null; then
    echo "Rolled back to $tag"
    exit 0
  fi
  sleep 2
done
echo "rollback image did not become ready" >&2
docker compose --env-file .env logs --tail 100 relaydock
exit 1
