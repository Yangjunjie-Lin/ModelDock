#!/usr/bin/env bash
set -Eeuo pipefail

# Compose/Swarm canary helper: deploy one candidate replica and verify probes.
# Run the migration Job separately before changing the application image.
root=${1:-/opt/relaydock}
tag=${2:?usage: canary.sh /opt/relaydock <immutable-image-tag>}
cd "$root"
[[ "$tag" =~ ^[A-Za-z0-9_.:@/-]+$ ]] || { echo "invalid image tag" >&2; exit 1; }
docker compose --env-file .env config --quiet
RELAYDOCK_IMAGE_TAG="$tag" docker compose --env-file .env up -d --no-deps --scale relaydock=1 relaydock
for _ in {1..60}; do
  if docker compose --env-file .env exec -T relaydock wget -qO- http://127.0.0.1:8080/readyz >/dev/null; then
    echo "Canary $tag is ready; continue with the rolling deployment."
    exit 0
  fi
  sleep 2
done
echo "Canary did not become ready; inspect logs before proceeding." >&2
exit 1
