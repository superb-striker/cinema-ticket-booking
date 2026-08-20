#!/bin/sh
set -eu

: "${DATABASE_URL:?set DATABASE_URL to the PostgreSQL connection URL}"
: "${BACKUP_DIR:?set BACKUP_DIR to a protected backup directory}"

umask 077
mkdir -p "$BACKUP_DIR"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="$BACKUP_DIR/cinema-$timestamp.dump"
temporary="$backup.partial"

cleanup() {
  rm -f "$temporary"
}
trap cleanup EXIT HUP INT TERM

pg_dump \
  --dbname="$DATABASE_URL" \
  --format=custom \
  --compress=9 \
  --no-owner \
  --no-acl \
  --file="$temporary"

pg_restore --list "$temporary" >/dev/null
mv "$temporary" "$backup"
(cd "$BACKUP_DIR" && sha256sum "$(basename "$backup")" >"$(basename "$backup").sha256")
trap - EXIT HUP INT TERM

printf 'Created and verified %s\n' "$backup"
