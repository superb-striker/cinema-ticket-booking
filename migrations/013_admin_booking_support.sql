ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN
    ('payment_pending', 'confirmed', 'payment_failed', 'expired', 'refund_pending', 'refunded', 'cancelled'));

CREATE TABLE booking_support_notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    author_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    note TEXT NOT NULL CHECK (btrim(note) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX booking_support_notes_order_idx
    ON booking_support_notes (order_id, created_at DESC);

