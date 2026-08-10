#!/usr/bin/env bash
set -Eeuo pipefail

root=${1:-/opt/relaydock}
cd "$root"
set -a
. ./.env
set +a

if [[ -z ${API_DOMAIN:-} || -z ${LETSENCRYPT_EMAIL:-} ]]; then
  echo "API_DOMAIN and LETSENCRYPT_EMAIL are required." >&2
  exit 1
fi
if [[ ! $API_DOMAIN =~ ^[A-Za-z0-9.-]+$ || $API_DOMAIN == *.example.com || $API_DOMAIN == example.com ]]; then
  echo "Configure a real DNS name you control before requesting HTTPS." >&2
  exit 1
fi

staging_args=()
if [[ ${LETSENCRYPT_STAGING:-false} == true ]]; then
  staging_args+=(--staging)
fi

docker compose --env-file .env run --rm --entrypoint certbot certbot \
  certonly --webroot -w /var/www/certbot \
  --email "$LETSENCRYPT_EMAIL" --agree-tos --no-eff-email \
  --keep-until-expiring "${staging_args[@]}" -d "$API_DOMAIN"

source_root=${RELAYDOCK_SOURCE_DIR:-./src}
sed "s/__DOMAIN__/${API_DOMAIN}/g" "$source_root/deploy/production/nginx/relaydock-tls.conf.template" >nginx/conf.d/relaydock.conf
docker compose --env-file .env exec nginx nginx -t
docker compose --env-file .env exec nginx nginx -s reload
curl -fsS "https://${API_DOMAIN}/healthz"
echo "HTTPS enabled for ${API_DOMAIN}. Certbot renews twice daily; Nginx reloads certificates every six hours."
