#!/bin/sh
# Postgres init script: one database per service (database-per-service).
# Runs once, when the postgres volume is first initialised.
set -eu

for db in users_db products_db orders_db payments_db inventory_db notifications_db; do
  echo "Creating database: $db"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
	SELECT 'CREATE DATABASE $db' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$db')\gexec
EOSQL
done
