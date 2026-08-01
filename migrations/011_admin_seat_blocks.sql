CREATE TABLE seat_blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    screen_id UUID NOT NULL,
    seat_id TEXT NOT NULL,
    screening_id UUID REFERENCES screenings(id) ON DELETE CASCADE,
    block_type TEXT NOT NULL CHECK (block_type IN ('maintenance', 'accessibility', 'other')),
    reason TEXT NOT NULL CHECK (btrim(reason) <> ''),
    starts_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT seat_blocks_seat_fkey FOREIGN KEY (screen_id, seat_id)
        REFERENCES seats(screen_id, id) ON DELETE CASCADE,
    CONSTRAINT seat_blocks_expiry_check CHECK (expires_at IS NULL OR expires_at > starts_at)
);

CREATE UNIQUE INDEX seat_blocks_global_active_unique
    ON seat_blocks (screen_id, seat_id)
    WHERE screening_id IS NULL AND released_at IS NULL;
CREATE UNIQUE INDEX seat_blocks_screening_active_unique
    ON seat_blocks (screening_id, seat_id)
    WHERE screening_id IS NOT NULL AND released_at IS NULL;
CREATE INDEX seat_blocks_availability_idx
    ON seat_blocks (screen_id, seat_id, screening_id, expires_at)
    WHERE released_at IS NULL;

