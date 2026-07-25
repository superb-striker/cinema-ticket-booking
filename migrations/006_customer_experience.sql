ALTER TABLE users
    ADD COLUMN phone TEXT,
    ADD CONSTRAINT users_phone_e164 CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{7,14}$');

CREATE TABLE password_reset_tokens (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_expiry_idx
    ON password_reset_tokens (expires_at) WHERE used_at IS NULL;

CREATE TABLE oauth_identities (
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject),
    UNIQUE (provider, user_id)
);

CREATE TABLE oauth_states (
    state_hash BYTEA PRIMARY KEY,
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX oauth_states_expiry_idx ON oauth_states (expires_at);

ALTER TABLE orders
    ADD COLUMN subtotal_paise BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN convenience_fee_paise BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN tax_paise BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN cancellation_requested_at TIMESTAMPTZ,
    ADD COLUMN cancellation_reason TEXT;

UPDATE orders SET subtotal_paise = amount_paise WHERE subtotal_paise = 0;
ALTER TABLE orders
    ADD CONSTRAINT orders_totals_match CHECK (
        amount_paise = subtotal_paise + convenience_fee_paise + tax_paise
    );

CREATE TABLE ticket_scans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id),
    scanned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    result TEXT NOT NULL CHECK (result IN ('admitted', 'duplicate', 'invalid'))
);
CREATE UNIQUE INDEX ticket_scans_single_admission_idx
    ON ticket_scans (order_id) WHERE result = 'admitted';

CREATE TABLE notification_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
    channel TEXT NOT NULL CHECK (channel IN ('email', 'sms')),
    recipient TEXT NOT NULL,
    template TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until TIMESTAMPTZ,
    last_error TEXT,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_id, channel, template)
);
CREATE INDEX notification_jobs_work_idx
    ON notification_jobs (next_attempt_at, created_at)
    WHERE status IN ('pending', 'failed');
