# Database migrations, backups, and recovery

PostgreSQL is the source of truth for accounts, orders, confirmed seats, payments, refund actions, policies, tickets, and administration data. Treat its recovery controls as part of the payment system.

## Migration policy

Migrations are ordered SQL files in `migrations/`. The migrator takes a PostgreSQL advisory lock, calculates a SHA-256 checksum for every file, applies each pending migration in its own transaction, and refuses to continue if an applied file has changed.

- Never edit an applied migration. Add a new forward migration.
- Prefer expand/migrate/contract changes so the previous application version remains compatible during rolling deployment and rollback.
- Back up production before destructive or high-volume schema changes.
- Test migrations against a recent, scrubbed production-size copy and record duration and locking behavior.
- Run `/app/migrate` once as a pre-deployment Job. Wait for success before updating application replicas.
- Inspect state with `DATABASE_URL=... ./scripts/check-migrations.sh`. The column is named `name`, not `version`.
- Do not automatically roll back schema. Correct failures with a reviewed forward migration or restore the database under the incident procedure.

## Backup policy

Use a managed PostgreSQL service with encrypted storage, continuous WAL archiving, point-in-time recovery, automated snapshots, cross-zone replication, and copies in a separate account or project. A reasonable starting objective is RPO 5 minutes and RTO 60 minutes; the business owner must approve the actual objectives.

In addition to PITR, take a daily logical backup and retain daily copies for 14 days, weekly copies for 8 weeks, and monthly copies according to legal and financial requirements. Encrypt backups with managed keys, restrict restore permission independently from database administration, enable object-lock/immutability where available, and alert on missing or failed backups.

Create a custom-format logical backup from a PostgreSQL client whose major version is at least the server version:

```sh
DATABASE_URL='postgres://...' \
BACKUP_DIR='/secure/cinema-backups' \
./scripts/backup-postgres.sh
```

The script writes with a restrictive umask, avoids ownership/ACL coupling, validates the archive catalog, and emits a SHA-256 sidecar. Transfer both files to encrypted off-host storage. A successful command is not proof of recoverability—perform an isolated restore exercise at least quarterly.

## Restore exercise

Always restore to a new, isolated database first:

```sh
createdb cinema_restore_test
DATABASE_URL='postgres://.../cinema_restore_test' \
BACKUP_FILE='/secure/cinema-backups/cinema-TIMESTAMP.dump' \
./scripts/restore-postgres.sh
```

The restore script verifies the checksum and archive, refuses a non-empty target by default, restores in a transaction, and analyzes the result. To deliberately replace a non-empty isolated target, set both `ALLOW_NONEMPTY_RESTORE=true` and `RESTORE_CONFIRM` to the exact target database name. Never use that override against production without an approved incident plan.

After restoring, verify:

1. All migration names and checksums match the release.
2. Row counts and recent orders, payments, payment events, tickets, and seat inventory are internally consistent.
3. Foreign keys and critical indexes exist, and `pg_amcheck` succeeds where available.
4. A temporary application instance becomes ready and can browse screenings, authenticate, and complete a provider test-mode booking.
5. Backup age, achieved RPO, restore duration, and evidence are recorded.

## Production recovery

1. Declare an incident, stop writes, preserve logs, and record the suspected failure time.
2. Choose the latest clean recovery point before corruption. Restore PITR into a new instance; never overwrite the only production copy.
3. Run consistency checks and payment reconciliation while access remains restricted.
4. Rotate credentials if compromise is possible, update private endpoints, run readiness and smoke checks, then shift traffic gradually.
5. Reconcile payments captured after the recovery point with Razorpay before reopening checkout. A provider capture must never be lost merely because its local webhook event was outside the restored window.
6. Retain the old database read-only until financial reconciliation and incident review are complete.

Redis recovery is separate and documented in `redis-operations.md`. Redis must not be used to reconstruct confirmed bookings.
