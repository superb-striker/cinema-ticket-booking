CREATE TABLE promotions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cinema_id UUID REFERENCES cinemas(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    discount_type TEXT NOT NULL CHECK (discount_type IN ('percentage', 'fixed')),
    discount_value BIGINT NOT NULL CHECK (discount_value > 0),
    minimum_subtotal_paise BIGINT NOT NULL DEFAULT 0 CHECK (minimum_subtotal_paise >= 0),
    maximum_discount_paise BIGINT CHECK (maximum_discount_paise > 0),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    maximum_redemptions INTEGER CHECK (maximum_redemptions > 0),
    per_customer_limit INTEGER NOT NULL DEFAULT 1 CHECK (per_customer_limit > 0),
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT promotions_code_normalized CHECK (code = upper(btrim(code))),
    CONSTRAINT promotions_period CHECK (ends_at > starts_at),
    CONSTRAINT promotions_percentage CHECK (discount_type <> 'percentage' OR discount_value <= 100)
);
CREATE UNIQUE INDEX promotions_code_unique ON promotions (code);
CREATE INDEX promotions_active_period_idx ON promotions (starts_at, ends_at) WHERE active;

ALTER TABLE orders DROP CONSTRAINT orders_totals_match;
ALTER TABLE orders
    ADD COLUMN promotion_id UUID REFERENCES promotions(id),
    ADD COLUMN promotion_code TEXT,
    ADD COLUMN discount_paise BIGINT NOT NULL DEFAULT 0 CHECK (discount_paise >= 0),
    ADD CONSTRAINT orders_discount_below_subtotal CHECK (discount_paise < subtotal_paise),
    ADD CONSTRAINT orders_totals_match CHECK (
        amount_paise = subtotal_paise + convenience_fee_paise + tax_paise - discount_paise
    );

CREATE TABLE coupon_redemptions (
    promotion_id UUID NOT NULL REFERENCES promotions(id),
    order_id UUID PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id),
    discount_paise BIGINT NOT NULL CHECK (discount_paise > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX coupon_redemptions_promotion_idx ON coupon_redemptions (promotion_id, created_at);
CREATE INDEX coupon_redemptions_customer_idx ON coupon_redemptions (promotion_id, user_id, created_at);

