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
public_site_domain=${RELAYDOCK_PUBLIC_SITE_DOMAIN:-}
if [[ -n $public_site_domain ]]; then
  if [[ ! $public_site_domain =~ ^[A-Za-z0-9.-]+$ || $public_site_domain == *.example.com || $public_site_domain == example.com ]]; then
    echo "Configure a real RELAYDOCK_PUBLIC_SITE_DOMAIN you control." >&2
    exit 1
  fi
  if [[ $public_site_domain == "$API_DOMAIN" ]]; then
    echo "RELAYDOCK_PUBLIC_SITE_DOMAIN must differ from API_DOMAIN; leave it empty for the legacy API-only entry." >&2
    exit 1
  fi
fi

staging_args=()
if [[ ${LETSENCRYPT_STAGING:-false} == true ]]; then
  staging_args+=(--staging)
fi

domain_args=(-d "$API_DOMAIN")
rendered_public_site_domain=public-site-disabled.invalid
if [[ -n $public_site_domain ]]; then
  domain_args+=(-d "$public_site_domain")
  rendered_public_site_domain=$public_site_domain
fi

docker compose --env-file .env run --rm --entrypoint certbot certbot \
  certonly --webroot -w /var/www/certbot \
  --email "$LETSENCRYPT_EMAIL" --agree-tos --no-eff-email \
  --keep-until-expiring --cert-name "$API_DOMAIN" "${staging_args[@]}" "${domain_args[@]}"

source_root=${RELAYDOCK_SOURCE_DIR:-./src}
sed -e "s/__API_DOMAIN__/${API_DOMAIN}/g" \
  -e "s/__PUBLIC_SITE_DOMAIN__/${rendered_public_site_domain}/g" \
  "$source_root/deploy/production/nginx/relaydock-tls.conf.template" >nginx/conf.d/relaydock.conf
docker compose --env-file .env exec nginx nginx -t
docker compose --env-file .env exec nginx nginx -s reload
curl -fsS "https://${API_DOMAIN}/healthz"
if [[ -n $public_site_domain ]]; then
  curl -fsS "https://${public_site_domain}/api/public/config" >/dev/null
fi
echo "HTTPS enabled for ${API_DOMAIN}. Certbot renews twice daily; Nginx reloads certificates every six hours."
