CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email)))
);
CREATE UNIQUE INDEX users_email_unique ON users (lower(email));

CREATE TABLE auth_sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX auth_sessions_user_id_idx ON auth_sessions (user_id);
CREATE INDEX auth_sessions_expiry_idx ON auth_sessions (expires_at);

CREATE TABLE movies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes > 0),
    language TEXT NOT NULL,
    certificate TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE screens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE seats (
    screen_id UUID NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    row_label TEXT NOT NULL,
    seat_number INTEGER NOT NULL CHECK (seat_number > 0),
    category TEXT NOT NULL DEFAULT 'standard',
    accessible BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (screen_id, id),
    UNIQUE (screen_id, row_label, seat_number)
);

CREATE TABLE screenings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    movie_id UUID NOT NULL REFERENCES movies(id),
    screen_id UUID NOT NULL REFERENCES screens(id),
    starts_at TIMESTAMPTZ NOT NULL,
    sales_end_at TIMESTAMPTZ NOT NULL,
    base_price_paise BIGINT NOT NULL CHECK (base_price_paise > 0),
    currency CHAR(3) NOT NULL DEFAULT 'INR' CHECK (currency = upper(currency)),
    status TEXT NOT NULL DEFAULT 'scheduled'
        CHECK (status IN ('scheduled', 'cancelled', 'completed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT screenings_sales_before_start CHECK (sales_end_at <= starts_at)
);
CREATE INDEX screenings_movie_time_idx ON screenings (movie_id, starts_at)
    WHERE status = 'scheduled';
CREATE INDEX screenings_screen_time_idx ON screenings (screen_id, starts_at);

CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    screening_id UUID NOT NULL REFERENCES screenings(id),
    hold_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN
        ('payment_pending', 'confirmed', 'payment_failed', 'expired', 'refund_pending', 'refunded')),
    amount_paise BIGINT NOT NULL CHECK (amount_paise > 0),
    currency CHAR(3) NOT NULL CHECK (currency = upper(currency)),
    provider TEXT NOT NULL,
    provider_order_id TEXT UNIQUE,
    provider_payment_id TEXT UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, idempotency_key)
);
CREATE INDEX orders_screening_status_idx ON orders (screening_id, status);
CREATE INDEX orders_user_created_idx ON orders (user_id, created_at DESC);
CREATE INDEX orders_pending_expiry_idx ON orders (expires_at)
    WHERE status = 'payment_pending';

CREATE TABLE order_seats (
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    screening_id UUID NOT NULL REFERENCES screenings(id),
    seat_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('payment_pending', 'confirmed', 'released')),
    price_paise BIGINT NOT NULL CHECK (price_paise > 0),
    PRIMARY KEY (order_id, seat_id)
);
-- This is the durable double-booking barrier. Released/failed rows remain for audit,
-- while only live payment attempts and confirmed tickets reserve inventory.
CREATE UNIQUE INDEX order_seats_live_inventory_unique
    ON order_seats (screening_id, seat_id)
    WHERE status IN ('payment_pending', 'confirmed');
CREATE INDEX order_seats_availability_idx
    ON order_seats (screening_id, status, seat_id);

CREATE TABLE payment_events (
    provider TEXT NOT NULL,
    event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    provider_order_id TEXT,
    provider_payment_id TEXT,
    payload JSONB NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, event_id)
);

CREATE TABLE payment_actions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id),
    action TEXT NOT NULL CHECK (action IN ('refund')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_id, action)
);
CREATE INDEX payment_actions_work_idx ON payment_actions (status, created_at)
    WHERE status IN ('pending', 'failed');

-- Typical cinema access pattern: one indexed lookup supplies both the occupied
-- seat map and an occupancy count for a particular screening.
CREATE VIEW screening_inventory AS
SELECT screening_id,
       count(*) FILTER (WHERE status = 'confirmed') AS confirmed_count,
       array_agg(seat_id ORDER BY seat_id) FILTER (WHERE status = 'confirmed') AS confirmed_seats,
       count(*) FILTER (WHERE status = 'payment_pending') AS payment_pending_count
FROM order_seats
GROUP BY screening_id;
