-- +goose Up
ALTER TABLE product_control_receipts
    ADD COLUMN resolved_state text NOT NULL DEFAULT '' CHECK (resolved_state IN ('','succeeded','failed','cancelled')),
    ADD COLUMN resolved_at timestamptz,
    ADD CONSTRAINT product_receipt_resolution CHECK ((resolved_state = '') = (resolved_at IS NULL));
-- Only verified successes have independent historical evidence. Missing
-- operation records never imply failure; older unresolved receipts need review.
UPDATE product_control_receipts SET resolved_state='succeeded', resolved_at=succeeded_at WHERE succeeded_at IS NOT NULL;
CREATE INDEX product_control_billing_period ON product_control_receipts(account_id,app_id,tenant_id,period,operation_id);

CREATE TABLE product_control_waivers (
    operation_id uuid PRIMARY KEY REFERENCES product_control_receipts(operation_id) ON DELETE CASCADE,
    reason text NOT NULL CHECK (octet_length(reason) BETWEEN 1 AND 512),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE product_control_billing_documents (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    period text NOT NULL CHECK (period ~ '^[0-9]{4}-[0-9]{2}$'),
    kind text NOT NULL CHECK (kind IN ('statement','credit_note')),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    amount_millicents bigint NOT NULL,
    statement_id uuid REFERENCES product_control_billing_documents(id),
    operation_id uuid REFERENCES product_control_receipts(operation_id),
    reason text NOT NULL DEFAULT '' CHECK (octet_length(reason) <= 512),
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_billing_document_kind CHECK (
        (kind='statement' AND amount_millicents>=0 AND statement_id IS NULL AND operation_id IS NULL AND reason='') OR
        (kind='credit_note' AND amount_millicents<0 AND statement_id IS NOT NULL AND operation_id IS NOT NULL AND reason<>''))
);
CREATE UNIQUE INDEX product_control_finalized_month ON product_control_billing_documents(app_id,tenant_id,period) WHERE kind='statement';
CREATE INDEX product_control_credit_operation ON product_control_billing_documents(operation_id) WHERE kind='credit_note';
CREATE INDEX product_control_documents_page ON product_control_billing_documents(account_id,app_id,tenant_id,period,id);

CREATE TABLE product_control_invoice_handoffs (
    document_id uuid PRIMARY KEY REFERENCES product_control_billing_documents(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    external_invoice_id text NOT NULL CHECK (octet_length(external_invoice_id) BETWEEN 1 AND 255),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id,external_invoice_id)
);
COMMENT ON TABLE product_control_invoice_handoffs IS 'One immutable owner invoicing reference per Product Control statement or credit note; no payment or refund is executed.';

-- +goose Down
DROP TABLE product_control_invoice_handoffs;
DROP TABLE product_control_billing_documents;
DROP TABLE product_control_waivers;
DROP INDEX product_control_billing_period;
ALTER TABLE product_control_receipts DROP CONSTRAINT product_receipt_resolution, DROP COLUMN resolved_at, DROP COLUMN resolved_state;
