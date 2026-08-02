CREATE INDEX orders_reporting_created_status_idx
    ON orders (created_at, status, screening_id);
CREATE INDEX orders_payment_failure_reporting_idx
    ON orders (created_at, status)
    WHERE status IN ('payment_failed', 'expired');
CREATE INDEX order_seats_reporting_screening_status_idx
    ON order_seats (screening_id, status);

