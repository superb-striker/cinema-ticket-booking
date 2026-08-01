ALTER TABLE screens ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE pricing_tiers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cinema_id UUID NOT NULL REFERENCES cinemas(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    seat_category TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pricing_tiers_name_nonempty CHECK (btrim(name) <> ''),
    CONSTRAINT pricing_tiers_category_nonempty CHECK (btrim(seat_category) <> ''),
    UNIQUE (cinema_id, name),
    UNIQUE (cinema_id, seat_category)
);

CREATE TABLE screening_prices (
    screening_id UUID NOT NULL REFERENCES screenings(id) ON DELETE CASCADE,
    pricing_tier_id UUID NOT NULL REFERENCES pricing_tiers(id),
    price_paise BIGINT NOT NULL CHECK (price_paise > 0),
    PRIMARY KEY (screening_id, pricing_tier_id)
);
CREATE INDEX screening_prices_screening_idx ON screening_prices (screening_id);

CREATE INDEX screens_cinema_active_idx ON screens (cinema_id, active);
CREATE INDEX pricing_tiers_cinema_active_idx ON pricing_tiers (cinema_id, active);
