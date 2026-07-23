CREATE TABLE cinemas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    timezone TEXT NOT NULL,
    address_line TEXT NOT NULL,
    city TEXT NOT NULL,
    region TEXT NOT NULL,
    postal_code TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX cinemas_city_active_idx ON cinemas (city,active);

INSERT INTO cinemas (id,name,timezone,address_line,city,region,postal_code)
VALUES ('40000000-0000-0000-0000-000000000001','Demo Cinema','Asia/Kolkata',
        '1 Cinema Road','Bengaluru','Karnataka','560001');

ALTER TABLE screens ADD COLUMN cinema_id UUID REFERENCES cinemas(id);
UPDATE screens SET cinema_id='40000000-0000-0000-0000-000000000001' WHERE cinema_id IS NULL;
ALTER TABLE screens ALTER COLUMN cinema_id SET NOT NULL;
ALTER TABLE screens DROP CONSTRAINT screens_name_key;
ALTER TABLE screens ADD CONSTRAINT screens_cinema_name_unique UNIQUE (cinema_id,name);

CREATE INDEX screenings_cinema_time_idx ON screenings (screen_id,starts_at)
    WHERE status='scheduled';
