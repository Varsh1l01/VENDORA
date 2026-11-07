#!/bin/sh
# Creates .env with freshly generated secrets (never commit .env).
set -eu
if [ -f .env ]; then
  echo ".env already exists; delete it first to regenerate." >&2
  exit 1
fi
rand() { head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c "$1"; }
sed \
  -e "s|^JWT_SECRET=.*|JWT_SECRET=$(rand 64)|" \
  -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$(rand 32)|" \
  -e "s|^REDIS_PASSWORD=.*|REDIS_PASSWORD=$(rand 32)|" \
  -e "s|^ADMIN_PASSWORD=.*|ADMIN_PASSWORD=$(rand 20)|" \
  .env.example > .env
echo "Wrote .env with generated secrets. Admin login: $(grep ^ADMIN_EMAIL .env | cut -d= -f2) / $(grep ^ADMIN_PASSWORD .env | cut -d= -f2)"
