#!/usr/bin/env bash
set -Eeuo pipefail

root=${1:-/opt/relaydock}
cd "$root"
docker compose --env-file .env config --quiet
docker compose --env-file .env build --pull
docker compose --env-file .env up -d --remove-orphans --scale relaydock="${RELAYDOCK_REPLICAS:-2}"
docker compose --env-file .env ps

for _ in {1..30}; do
  if docker compose --env-file .env exec -T relaydock wget -qO- http://127.0.0.1:8080/readyz >/dev/null; then
    echo "RelayDock replicas are ready through the internal probe."
    exit 0
  fi
  sleep 2
done
echo "RelayDock did not become ready within 60 seconds." >&2
docker compose --env-file .env logs --tail 100 relaydock postgres redis
exit 1
