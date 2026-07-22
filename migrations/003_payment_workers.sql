ALTER TABLE orders
    ADD COLUMN next_reconcile_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN reconcile_locked_until TIMESTAMPTZ,
    ADD COLUMN reconciliation_attempts INTEGER NOT NULL DEFAULT 0;

CREATE INDEX orders_reconciliation_work_idx
    ON orders (next_reconcile_at, created_at)
    WHERE provider_order_id IS NOT NULL
      AND status IN ('payment_pending', 'expired', 'payment_failed');

ALTER TABLE payment_actions
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN locked_until TIMESTAMPTZ,
    ADD COLUMN provider_action_id TEXT;

CREATE UNIQUE INDEX payment_actions_provider_id_unique
    ON payment_actions (provider_action_id)
    WHERE provider_action_id IS NOT NULL;

DROP INDEX payment_actions_work_idx;
CREATE INDEX payment_actions_work_idx
    ON payment_actions (next_attempt_at, created_at)
    WHERE status IN ('pending', 'failed');

