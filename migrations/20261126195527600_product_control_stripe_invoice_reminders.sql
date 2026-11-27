-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE product_control_stripe_invoice_sends
    ADD COLUMN IF NOT EXISTS reminder_hash text,
    ADD COLUMN IF NOT EXISTS reminder_started_at timestamptz,
    ADD COLUMN IF NOT EXISTS reminder_sent_at timestamptz;

-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'product_control_stripe_invoice_sends'::regclass
          AND conname = 'product_control_stripe_invoice_sends_reminder_chk'
    ) THEN
        ALTER TABLE product_control_stripe_invoice_sends
            ADD CONSTRAINT product_control_stripe_invoice_sends_reminder_chk CHECK (
                (reminder_hash IS NULL AND reminder_started_at IS NULL AND reminder_sent_at IS NULL) OR
                (stage = 'sent' AND reminder_hash IS NOT NULL AND length(reminder_hash) = 64
                 AND reminder_started_at IS NOT NULL
                 AND (reminder_sent_at IS NULL OR reminder_sent_at >= reminder_started_at))
            );
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE product_control_stripe_invoice_sends
    DROP CONSTRAINT product_control_stripe_invoice_sends_reminder_chk,
    DROP COLUMN reminder_sent_at,
    DROP COLUMN reminder_started_at,
    DROP COLUMN reminder_hash;
