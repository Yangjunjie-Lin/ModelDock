#!/usr/bin/env bash
set -Eeuo pipefail

root=${1:-/opt/relaydock}
source_root=${2:-$root/src}
if [[ ${EUID} -ne 0 ]]; then
  echo "Run as root: sudo $0 $root" >&2
  exit 1
fi
if [[ ! -f $root/.env || ! -f $source_root/docker-compose.production.yml ]]; then
  echo "Clone RelayDock into $source_root and generate $root/.env before preparing the host." >&2
  exit 1
fi

set -a
. "$root/.env"
set +a
if [[ -z ${API_DOMAIN:-} || ! $API_DOMAIN =~ ^[A-Za-z0-9.-]+$ || $API_DOMAIN == *.example.com || $API_DOMAIN == example.com ]]; then
  echo "API_DOMAIN must be a real FQDN you control." >&2
  exit 1
fi
public_site_domain=${RELAYDOCK_PUBLIC_SITE_DOMAIN:-public-site-disabled.invalid}
if [[ $public_site_domain != public-site-disabled.invalid ]]; then
  if [[ ! $public_site_domain =~ ^[A-Za-z0-9.-]+$ || $public_site_domain == *.example.com || $public_site_domain == example.com ]]; then
    echo "RELAYDOCK_PUBLIC_SITE_DOMAIN must be a real FQDN you control." >&2
    exit 1
  fi
  if [[ $public_site_domain == "$API_DOMAIN" ]]; then
    echo "RELAYDOCK_PUBLIC_SITE_DOMAIN must differ from API_DOMAIN; leave it empty to preserve the legacy API-only entry." >&2
    exit 1
  fi
fi

install -d -m 0750 \
  "$root/nginx/conf.d" \
  "$root/data/postgres" \
  "$root/data/redis" \
  "$root/data/prometheus" \
  "$root/data/alertmanager" \
  "$root/data/relaydock/logs" \
  "$root/data/nginx/logs" \
  "$root/data/certbot/conf" \
  "$root/data/certbot/www" \
  "$root/data/certbot/logs" \
  "$root/data/backups"

install -m 0644 "$source_root/docker-compose.production.yml" "$root/docker-compose.yml"
install -m 0644 "$source_root/deploy/production/nginx/nginx.conf" "$root/nginx/nginx.conf"
sed -e "s/__API_DOMAIN__/${API_DOMAIN}/g" \
  -e "s/__PUBLIC_SITE_DOMAIN__/${public_site_domain}/g" \
  "$source_root/deploy/production/nginx/relaydock-http.conf.template" >"$root/nginx/conf.d/relaydock.conf"
chmod 0644 "$root/nginx/conf.d/relaydock.conf"

docker pull postgres:17-alpine redis:7.4-alpine nginx:1.27-alpine certbot/certbot:v4.0.0
postgres_uid=$(docker run --rm postgres:17-alpine id -u postgres)
postgres_gid=$(docker run --rm postgres:17-alpine id -g postgres)
redis_uid=$(docker run --rm redis:7.4-alpine id -u redis)
redis_gid=$(docker run --rm redis:7.4-alpine id -g redis)
chown -R "$postgres_uid:$postgres_gid" "$root/data/postgres"
chown -R "$redis_uid:$redis_gid" "$root/data/redis"
chown -R 65534:65534 "$root/data/prometheus" "$root/data/alertmanager"
chown -R 10001:10001 "$root/data/relaydock/logs"
chmod 0700 "$root/data/postgres" "$root/data/redis"
chmod 0750 "$root/data/prometheus" "$root/data/alertmanager"
chmod 0750 "$root/data/relaydock/logs" "$root/data/nginx/logs" "$root/data/backups"
chmod 0600 "$root/.env"
chmod 0755 "$source_root/deploy/production/scripts/"*.sh

install -m 0644 "$source_root/deploy/production/systemd/relaydock-compose.service" /etc/systemd/system/relaydock-compose.service
systemctl daemon-reload
systemctl enable relaydock-compose.service

cd "$root"
docker compose --env-file .env config --quiet
echo "Production directories, Nginx bootstrap configuration, and systemd fallback are ready."
