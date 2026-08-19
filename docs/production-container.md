# Production container

The multi-stage Dockerfile produces a minimal distroless image containing only the API, migrator, health-check binary, static assets, migrations, and CA certificates. It runs as UID/GID 65532 with no shell. Builder, Dockerfile frontend, and runtime images are digest-pinned; dependency updates should deliberately refresh those digests and pass CI scanning.

Build and identify an immutable image:

```sh
docker build \
  --build-arg VERSION="$(git describe --always --dirty)" \
  --build-arg VCS_REF="$(git rev-parse HEAD)" \
  -t registry.example.com/cinema/cinema-api:VERSION .
docker inspect --format='{{index .RepoDigests 0}}' registry.example.com/cinema/cinema-api:VERSION
```

Set `CINEMA_IMAGE` to the resulting registry digest (`repository@sha256:...`) before using `docker-compose.prod.yaml`. Supply the remaining required settings from a secret manager or protected environment file that is not committed. Run:

```sh
docker compose -f docker-compose.prod.yaml config
docker compose -f docker-compose.prod.yaml run --rm migrate
docker compose -f docker-compose.prod.yaml up -d --no-deps app
```

The production definition does not start PostgreSQL, Redis, or Redis Commander. It expects private managed data services, publishes the API on loopback for a TLS reverse proxy, uses a read-only filesystem and dropped capabilities, and allows 30 seconds for request draining. `/app/healthcheck` queries `/health/ready` without requiring a shell.

Never use `docker-compose.yaml` as a production definition: its credentials and loopback data services are intentionally local-development defaults.
