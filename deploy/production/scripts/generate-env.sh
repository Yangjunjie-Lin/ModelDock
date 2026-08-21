#!/usr/bin/env bash
set -Eeuo pipefail

root=${1:-/opt/relaydock}
domain=${2:-}
admin_email=${3:-}
letsencrypt_email=${4:-$admin_email}
public_site_domain=${5:-}

if [[ -z $domain || -z $admin_email || -z $letsencrypt_email ]]; then
  echo "Usage: $0 /opt/relaydock api.your-domain.com admin@your-domain.com [acme-email] [public-site.your-domain.com]" >&2
  exit 1
fi
if [[ ! $domain =~ ^[A-Za-z0-9.-]+$ || $domain == *.example.com || $domain == example.com ]]; then
  echo "Use a real FQDN you control; example.com cannot receive a production certificate." >&2
  exit 1
fi
if [[ -n $public_site_domain ]]; then
  if [[ ! $public_site_domain =~ ^[A-Za-z0-9.-]+$ || $public_site_domain == *.example.com || $public_site_domain == example.com || $public_site_domain == "$domain" ]]; then
    echo "The optional public-site FQDN must be real and different from the API FQDN." >&2
    exit 1
  fi
fi
if [[ -e $root/.env ]]; then
  echo "$root/.env already exists; refusing to overwrite secrets." >&2
  exit 1
fi

umask 077
postgres_password=$(openssl rand -hex 24)
redis_password=$(openssl rand -hex 24)
master_key=$(openssl rand -base64 32 | tr -d '\n')
hmac_secret=$(openssl rand -hex 32)
jwt_secret=$(openssl rand -hex 32)
admin_password=$(openssl rand -base64 30 | tr -d '\n')

cat >"$root/.env" <<EOF
COMPOSE_PROJECT_NAME=relaydock
RELAYDOCK_IMAGE_TAG=production
RELAYDOCK_SOURCE_DIR=./src
TZ=Asia/Tokyo
API_DOMAIN=$domain
RELAYDOCK_PUBLIC_SITE_DOMAIN=$public_site_domain
LETSENCRYPT_EMAIL=$letsencrypt_email
RELAYDOCK_PUBLIC_CONSOLE_URL=https://${public_site_domain:-$domain}
RELAYDOCK_PUBLIC_SUPPORT_EMAIL=${RELAYDOCK_PUBLIC_SUPPORT_EMAIL:-support@example.invalid}
RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL=${RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL:-enterprise@example.invalid}
ALLOWED_ORIGINS=https://${public_site_domain:-$domain}
TRUSTED_PROXIES=172.16.0.0/12,127.0.0.1/32
GO_MODULE_PROXY=https://proxy.golang.org,direct
POSTGRES_DB=relaydock
POSTGRES_USER=relaydock
POSTGRES_PASSWORD=$postgres_password
DATABASE_URL=postgres://relaydock:$postgres_password@postgres:5432/relaydock?sslmode=disable
POSTGRES_MAX_CONNS=20
POSTGRES_MIN_CONNS=2
POSTGRES_MAX_CONN_IDLE_TIME=5m
POSTGRES_MAX_CONN_LIFETIME=30m
REDIS_PASSWORD=$redis_password
REDIS_URL=redis://:$redis_password@redis:6379/0
REDIS_POOL_SIZE=20
REDIS_MIN_IDLE_CONNS=2
REDIS_DIAL_TIMEOUT=5s
REDIS_READ_TIMEOUT=3s
REDIS_WRITE_TIMEOUT=3s
RELAYDOCK_MASTER_KEY=$master_key
RELAYDOCK_API_KEY_HMAC_SECRET=$hmac_secret
RELAYDOCK_JWT_SECRET=$jwt_secret
RELAYDOCK_JWT_LIFETIME=15m
RELAYDOCK_JWT_REFRESH_LIFETIME=168h
RELAYDOCK_PUBLIC_FUNNEL_RATE_LIMIT=120
RELAYDOCK_ADMIN_EMAIL=$admin_email
RELAYDOCK_ADMIN_PASSWORD=$admin_password
RELAYDOCK_ADMIN_DISPLAY_NAME=RelayDock Administrator
GATEWAY_DIAGNOSTIC_PORT=8080
CONTROL_PLANE_PORT=8081
ADMIN_WEB_PORT=3000
CONSOLE_WEB_PORT=3001
LOG_LEVEL=info
MAX_REQUEST_BODY_BYTES=10485760
CREDENTIAL_COOLDOWN=30s
SHUTDOWN_TIMEOUT=30s
WEBHOOK_TIMEOUT=10s
WEBHOOK_POLL_INTERVAL=2s
WEBHOOK_MAX_ATTEMPTS=6
EOF
chmod 0600 "$root/.env"

printf 'Generated %s/.env\nInitial administrator: %s\nInitial password: %s\nStore this password now; it will not be printed again.\n' "$root" "$admin_email" "$admin_password"
