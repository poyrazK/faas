-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS product_control_stripe_invoice_sends (
    export_id uuid PRIMARY KEY REFERENCES usage_statement_invoice_exports(id) ON DELETE CASCADE,
    review_hash text NOT NULL CHECK (length(review_hash) = 64),
    terms_hash text NOT NULL CHECK (length(terms_hash) = 64),
    due_at timestamptz NOT NULL,
    stage text NOT NULL,
    send_started_at timestamptz,
    CONSTRAINT product_control_stripe_invoice_sends_stage_chk CHECK (
        (stage IN ('preparing', 'finalizing') AND send_started_at IS NULL) OR
        (stage IN ('sending', 'sent') AND send_started_at IS NOT NULL)
    )
);

-- +goose Down
DROP TABLE product_control_stripe_invoice_sends;
