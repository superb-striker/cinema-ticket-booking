# Redis operations

Redis stores short-lived seat holds and distributed rate-limit state. PostgreSQL remains the source of truth for confirmed bookings, but losing Redis can release active holds early and temporarily weaken rate limiting.

## Production baseline

- Place Redis on a private network and allow connections only from the application and monitoring systems.
- Require ACL credentials. Supply `REDIS_USERNAME` when the provider uses named users and a secret of at least 24 characters in `REDIS_PASSWORD`.
- Set `REDIS_TLS_ENABLED=true`. Set `REDIS_TLS_SERVER_NAME` when it differs from `REDIS_ADDR`; use `REDIS_TLS_CA_FILE` for a private CA and the certificate/key options for mTLS.
- Use a replicated managed Redis service with automatic failover. Do not expose port 6379 publicly.
- Keep AOF persistence enabled with `appendfsync everysec` and periodic RDB snapshots. The development Compose service uses this policy and stores `/data` in the `redis-data` volume.
- Keep eviction disabled (`maxmemory-policy noeviction`) or reserve enough memory that booking keys cannot be evicted. Holds expire by TTL.

## Monitoring and alerts

Scrape the protected `/metrics` endpoint. Alert on sustained increases in `cinema_redis_pool_timeouts_total`, a pool with no idle connections, readiness probe failures, memory above 80%, rejected connections, replication lag, and persistence failures. Provider monitoring should also cover `master_link_status`, `master_last_io_seconds_ago`, AOF rewrite failures, and the age of the latest successful snapshot.

The application readiness endpoint pings Redis and PostgreSQL. Remove an unhealthy instance from traffic; do not route bookings to a process that is not ready.

## Backup and restore

For self-managed Redis, schedule encrypted off-host copies of both the RDB snapshot and AOF files. Take a filesystem or volume snapshot only after `BGSAVE` reports success, or use `redis-cli --rdb` against a replica to avoid load on the primary. Test restores quarterly in an isolated network.

To restore:

1. Stop application workers and block booking traffic.
2. Start a matching Redis version in an isolated environment with an empty data directory.
3. Restore `appendonlydir`/AOF and the most recent RDB according to Redis documentation, then start Redis and inspect its startup log.
4. Verify `PING`, ACLs, replication health, persistence status, representative hold TTLs, and key counts.
5. Point one application instance at the restored node, confirm `/health/ready`, then progressively restore traffic.

Because holds are intentionally short-lived, a restore older than the hold TTL should be treated as having no reliable active holds. Reconcile confirmed inventory from PostgreSQL and allow stale hold keys to expire; never infer a confirmed booking from Redis.

## Incident recovery

If Redis is unavailable, readiness fails and checkout/hold requests must stop. Restore or fail over Redis, verify TLS and credentials, then restart or reconnect application instances. After recovery, compare recent `payment_pending` orders with provider state and PostgreSQL seat inventory. Do not manually extend hold TTLs or promote Redis data into confirmed inventory.

Redis Commander is a local diagnostic tool only. It is behind the `debug` Compose profile, binds to loopback, and requires HTTP credentials. Never deploy that service in production; use provider consoles through audited, time-limited access instead.
