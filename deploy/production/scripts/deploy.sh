#!/usr/bin/env bash
set -Eeuo pipefail

root=${1:-/opt/relaydock}
cd "$root"
docker compose --env-file .env config --quiet
docker compose --env-file .env build --pull
docker compose --env-file .env up -d --remove-orphans
docker compose --env-file .env ps

for _ in {1..30}; do
  if curl -fsS http://127.0.0.1:8080/readyz >/dev/null; then
    echo "RelayDock is ready on the loopback diagnostic endpoint."
    exit 0
  fi
  sleep 2
done
echo "RelayDock did not become ready within 60 seconds." >&2
docker compose --env-file .env logs --tail 100 relaydock postgres redis
exit 1
