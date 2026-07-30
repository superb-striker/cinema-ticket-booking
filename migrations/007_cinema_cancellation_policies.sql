CREATE TABLE cinema_cancellation_policies (
    cinema_id UUID PRIMARY KEY REFERENCES cinemas(id) ON DELETE CASCADE,
    cutoff_minutes INTEGER NOT NULL CHECK (cutoff_minutes BETWEEN 0 AND 10080),
    refund_percent INTEGER NOT NULL CHECK (refund_percent BETWEEN 1 AND 100),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO cinema_cancellation_policies (cinema_id, cutoff_minutes, refund_percent)
SELECT id, 120, 100 FROM cinemas
ON CONFLICT (cinema_id) DO NOTHING;

ALTER TABLE orders
    ADD COLUMN cancellation_cutoff_minutes INTEGER NOT NULL DEFAULT 120
        CHECK (cancellation_cutoff_minutes BETWEEN 0 AND 10080),
    ADD COLUMN cancellation_refund_percent INTEGER NOT NULL DEFAULT 100
        CHECK (cancellation_refund_percent BETWEEN 1 AND 100),
    ADD COLUMN cancellation_refund_amount_paise BIGINT
        CHECK (cancellation_refund_amount_paise IS NULL OR cancellation_refund_amount_paise > 0);

COMMENT ON COLUMN orders.cancellation_cutoff_minutes IS
    'Immutable snapshot of the cinema cancellation cutoff when checkout began';
COMMENT ON COLUMN orders.cancellation_refund_percent IS
    'Immutable snapshot of the cinema refund percentage when checkout began';
