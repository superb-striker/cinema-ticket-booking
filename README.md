# Cinema ticket booking

A Go booking service with PostgreSQL-backed orders, Redis-backed expiring seat holds, secure cookie authentication, and Razorpay Checkout for UPI and other Indian payment methods.

## Local setup

1. Copy `.env.example` to `.env` and enter Razorpay **test-mode** credentials.
2. Start the stack with `docker compose up --build`.
3. Open <http://localhost:8080> and create an account.
4. Configure a Razorpay test webhook pointing to `https://your-public-test-host/api/v1/webhooks/razorpay`, subscribe to `payment.captured`, and use the same webhook secret as `.env`.

The SQL files under `migrations/` are applied by PostgreSQL only when the local volume is first created. Production deployments should run them through a migration job rather than mounting them into the database container.

## Inventory model

- Redis atomically reserves up to ten seats for ten minutes. All keys for one screening share a Redis Cluster hash tag, allowing the multi-seat Lua operation to remain atomic.
- PostgreSQL is authoritative once checkout begins. A partial unique index on `(screening_id, seat_id)` prevents live orders from double-booking inventory.
- `GET /api/v1/screenings/{id}/seats` uses the covering inventory index for the customer seat map.
- The `screening_inventory` view supplies confirmed-seat counts and exact seat IDs for cinema operations and reporting.
- Captured Razorpay webhooks are signature-, amount-, currency-, and event-ID-validated. Late payments enter `refund_pending` and enqueue an idempotent refund action instead of silently booking an already released seat.

## API flow

1. `POST /api/v1/auth/register` or `/login` with `email` and `password`.
2. `GET /api/v1/screenings`.
3. `GET /api/v1/screenings/{screeningID}/seats`.
4. `POST /api/v1/screenings/{screeningID}/holds` with `{"seat_ids":["A1","A2"]}`.
5. `POST /api/v1/screenings/{screeningID}/holds/{holdID}/checkout` with a unique `Idempotency-Key` header.
6. Open Razorpay Checkout using the returned `checkout_key` and `order.provider_order_id`.
7. Send the Checkout callback to `POST /api/v1/orders/{orderID}/verify-payment`.
8. Poll `GET /api/v1/orders/{orderID}` until the signed `payment.captured` webhook changes the order to `confirmed`.

## Required production work

- Put the service behind HTTPS and keep `COOKIE_SECURE=true`.
- Store secrets in a managed secret store and use managed PostgreSQL/Redis with private networking.
- Run the pending-refund worker and operational alerts before accepting live funds.
- Add email verification, password reset, rate limiting, observability, backups, and provider reconciliation jobs.
- Pin container images by digest in deployment manifests and run migrations as a controlled release step.
