INSERT INTO movies (id, slug, title, duration_minutes, language, certificate)
VALUES
    ('10000000-0000-0000-0000-000000000001', 'inception', 'Inception', 148, 'English', 'UA'),
    ('10000000-0000-0000-0000-000000000002', 'dune-part-two', 'Dune: Part Two', 166, 'English', 'UA')
ON CONFLICT DO NOTHING;

INSERT INTO screens (id, name)
VALUES ('20000000-0000-0000-0000-000000000001', 'Screen 1')
ON CONFLICT DO NOTHING;

INSERT INTO seats (screen_id, id, row_label, seat_number)
SELECT '20000000-0000-0000-0000-000000000001', chr(64 + r) || n::text, chr(64 + r), n
FROM generate_series(1, 5) AS r
CROSS JOIN generate_series(1, 8) AS n
ON CONFLICT DO NOTHING;

INSERT INTO screenings
    (id, movie_id, screen_id, starts_at, sales_end_at, base_price_paise, currency)
VALUES
    ('30000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
     '20000000-0000-0000-0000-000000000001', now() + interval '7 days',
     now() + interval '7 days' - interval '10 minutes', 35000, 'INR'),
    ('30000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000002',
     '20000000-0000-0000-0000-000000000001', now() + interval '8 days',
     now() + interval '8 days' - interval '10 minutes', 40000, 'INR')
ON CONFLICT DO NOTHING;
