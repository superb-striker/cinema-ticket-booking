# Cinema ticket booking

A Go booking service with PostgreSQL-backed orders, Redis-backed expiring seat holds, secure cookie authentication, and Razorpay Checkout for UPI and other Indian payment methods.

## Local setup

1. Copy `.env.example` to `.env` and enter Razorpay **test-mode** credentials.
2. Start the stack with `docker compose up --build`.
3. Open <http://localhost:8080> and create an account.
4. Configure a Razorpay test webhook pointing to `https://your-public-test-host/api/v1/webhooks/razorpay`, subscribe to `payment.captured`, and use the same webhook secret as `.env`.

The one-shot `migrate` service applies pending SQL migrations transactionally before the application starts. It uses a PostgreSQL advisory lock, so only one deployment can migrate at a time. Run the same `/app/migrate` command as a release job in production.

## Inventory model

- Redis atomically reserves up to ten seats for ten minutes. All keys for one screening share a Redis Cluster hash tag, allowing the multi-seat Lua operation to remain atomic.
- PostgreSQL is authoritative once checkout begins. A partial unique index on `(screening_id, seat_id)` prevents live orders from double-booking inventory.
- Cinemas own screens, and screen names are unique within a cinema. Cinema/city metadata is returned with each screening and indexed for location-oriented discovery.
- `GET /api/v1/screenings/{id}/seats` uses the covering inventory index for the customer seat map.
- The `screening_inventory` view supplies confirmed-seat counts and exact seat IDs for cinema operations and reporting.
- Captured Razorpay webhooks are signature-, amount-, currency-, and event-ID-validated.
- A background reconciliation worker recovers captured payments if a webhook is delayed or lost.
- Late payments enter `refund_pending`; a leased, retrying worker issues a full refund and records the Razorpay refund ID instead of silently booking an already released seat.
- Expired order inventory is released independently of customer traffic.

## Abuse protection

Registration and login are limited per client IP. Holds, checkout, and payment verification are limited per authenticated user using a distributed Redis token bucket. Set `TRUSTED_PROXY_CIDRS` to the comma-separated CIDRs of load balancers that are allowed to supply `X-Forwarded-For`; forwarded headers from all other peers are ignored.

## API flow

1. `POST /api/v1/auth/register` or `/login` with `email` and `password`.
2. `GET /api/v1/screenings`.
3. `GET /api/v1/screenings/{screeningID}/seats`.
4. `POST /api/v1/screenings/{screeningID}/holds` with `{"seat_ids":["A1","A2"]}`.
5. `POST /api/v1/screenings/{screeningID}/holds/{holdID}/checkout` with a unique `Idempotency-Key` header.
6. Open Razorpay Checkout using the returned `checkout_key` and `order.provider_order_id`.
7. Send the Checkout callback to `POST /api/v1/orders/{orderID}/verify-payment`.
8. Poll `GET /api/v1/orders/{orderID}` until a signed webhook or Razorpay API reconciliation changes the order to `confirmed`.

## Required production work

- Put the service behind HTTPS and keep `COOKIE_SECURE=true`.
- Store secrets in a managed secret store and use managed PostgreSQL/Redis with private networking.
- Add email verification and password reset before public account registration.
- Add structured observability and alerts for failed refunds, reconciliation errors, and queue latency.
- Configure automated PostgreSQL backups and restore drills.
- Pin container images by digest in deployment manifests.
