CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE screens
    ADD COLUMN format TEXT NOT NULL DEFAULT '2D',
    ADD CONSTRAINT screens_format_not_blank CHECK (btrim(format) <> '');

CREATE INDEX movies_title_search_idx ON movies USING gin (title gin_trgm_ops)
    WHERE active;
CREATE INDEX movies_language_idx ON movies (lower(language))
    WHERE active;
CREATE INDEX cinemas_city_search_idx ON cinemas (lower(city), name)
    WHERE active;
CREATE INDEX screens_format_idx ON screens (lower(format), cinema_id);
