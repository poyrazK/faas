-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE product_control_stripe_invoice_sends
    ADD COLUMN IF NOT EXISTS send_event_id text,
    ADD COLUMN IF NOT EXISTS send_event_at timestamptz;

-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'product_control_stripe_invoice_sends'::regclass
          AND conname = 'product_control_stripe_invoice_sends_event_chk'
    ) THEN
        ALTER TABLE product_control_stripe_invoice_sends
            ADD CONSTRAINT product_control_stripe_invoice_sends_event_chk CHECK (
                (send_event_id IS NULL AND send_event_at IS NULL) OR
                (stage = 'sent' AND send_event_id IS NOT NULL AND send_event_at IS NOT NULL
                 AND send_event_id ~ '^evt_[A-Za-z0-9]{1,120}$'
                 AND send_event_at >= date_trunc('second', send_started_at))
            );
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE product_control_stripe_invoice_sends
    DROP CONSTRAINT product_control_stripe_invoice_sends_event_chk,
    DROP COLUMN send_event_at,
    DROP COLUMN send_event_id;
