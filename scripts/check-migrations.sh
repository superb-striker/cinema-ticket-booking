#!/bin/sh
set -eu

: "${DATABASE_URL:?set DATABASE_URL to the PostgreSQL connection URL}"

psql "$DATABASE_URL" -X --set=ON_ERROR_STOP=1 <<'SQL'
SELECT name, checksum, applied_at
FROM schema_migrations
ORDER BY name;
SQL
