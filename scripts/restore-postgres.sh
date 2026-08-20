#!/bin/sh
set -eu

: "${DATABASE_URL:?set DATABASE_URL to the restore target}"
: "${BACKUP_FILE:?set BACKUP_FILE to a custom-format pg_dump file}"

if [ ! -r "$BACKUP_FILE" ]; then
  printf 'Backup is not readable: %s\n' "$BACKUP_FILE" >&2
  exit 1
fi

if [ -f "$BACKUP_FILE.sha256" ]; then
  (cd "$(dirname "$BACKUP_FILE")" && sha256sum -c "$(basename "$BACKUP_FILE").sha256")
else
  printf 'Refusing an unverified backup; expected %s.sha256\n' "$BACKUP_FILE" >&2
  exit 1
fi

pg_restore --list "$BACKUP_FILE" >/dev/null
database_name=$(psql "$DATABASE_URL" -XAtc 'select current_database()')
table_count=$(psql "$DATABASE_URL" -XAtc "select count(*) from pg_catalog.pg_tables where schemaname not in ('pg_catalog','information_schema')")

if [ "$table_count" != "0" ]; then
  if [ "${ALLOW_NONEMPTY_RESTORE:-false}" != "true" ]; then
    printf 'Refusing to overwrite non-empty database %s; restore into an empty database\n' "$database_name" >&2
    exit 1
  fi
  if [ "${RESTORE_CONFIRM:-}" != "$database_name" ]; then
    printf 'Set RESTORE_CONFIRM=%s to acknowledge destructive replacement\n' "$database_name" >&2
    exit 1
  fi
  pg_restore --dbname="$DATABASE_URL" --exit-on-error --single-transaction \
    --no-owner --no-acl --clean --if-exists "$BACKUP_FILE"
else
  pg_restore --dbname="$DATABASE_URL" --exit-on-error --single-transaction \
    --no-owner --no-acl "$BACKUP_FILE"
fi

psql "$DATABASE_URL" -X --set=ON_ERROR_STOP=1 -c 'ANALYZE;'
printf 'Restored %s into %s\n' "$BACKUP_FILE" "$database_name"
