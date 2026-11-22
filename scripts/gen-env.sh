#!/usr/bin/env sh
# Creates .env from .env.example with freshly generated secrets.
# Refuses to overwrite an existing .env.
set -eu
cd "$(dirname "$0")/.."

if [ -f .env ]; then
  echo ".env already exists; not overwriting. Delete it first to regenerate." >&2
  exit 1
fi

rand_b64() { openssl rand -base64 "$1" | tr -d '\n'; }
rand_url() { openssl rand -base64 48 | tr -d '\n/+=' | cut -c1-"$1"; }

ENCRYPTION_KEY=$(rand_b64 32)
API_KEY_PEPPER=$(rand_url 48)
POSTGRES_PASSWORD=$(rand_url 24)
GRAFANA_ADMIN_PASSWORD=$(rand_url 20)
ADMIN_PASSWORD=$(rand_url 20)
DEMO_API_KEY="gwk_$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n')"

sed -e "s|^ENCRYPTION_KEY=.*|ENCRYPTION_KEY=${ENCRYPTION_KEY}|" \
    -e "s|^API_KEY_PEPPER=.*|API_KEY_PEPPER=${API_KEY_PEPPER}|" \
    -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${POSTGRES_PASSWORD}|" \
    -e "s|^GRAFANA_ADMIN_PASSWORD=.*|GRAFANA_ADMIN_PASSWORD=${GRAFANA_ADMIN_PASSWORD}|" \
    -e "s|^ADMIN_PASSWORD=.*|ADMIN_PASSWORD=${ADMIN_PASSWORD}|" \
    -e "s|^DEMO_API_KEY=.*|DEMO_API_KEY=${DEMO_API_KEY}|" \
    .env.example > .env
chmod 600 .env

echo "Created .env"
echo "  Console login : admin@example.com / ${ADMIN_PASSWORD}"
echo "  Grafana login : admin / ${GRAFANA_ADMIN_PASSWORD}"
echo "  Demo API key  : stored as DEMO_API_KEY in .env"
