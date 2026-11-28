-- filename: 20261128145617605_egress_flow_log_blocked.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- ADR-371 addendum: the egress flow log also records blocked attempts.
-- vmmd reads each instance's egress_blocked nft set, which the policy drops
-- (internal and metadata addresses, SMTP, ports outside the plan, unresolved
-- destinations, anything outside an allowlist) fill with the destination,
-- protocol and port of a guest TCP or UDP new flow they dropped.
--
-- Both columns have constant defaults, so existing rows and older vmmd
-- writers read and write as allowed TCP flows. The checks are NOT VALID:
-- existing rows already satisfy them and the table is not rescanned.
ALTER TABLE egress_flow_log ADD COLUMN IF NOT EXISTS blocked  boolean NOT NULL DEFAULT false;
ALTER TABLE egress_flow_log ADD COLUMN IF NOT EXISTS protocol text    NOT NULL DEFAULT 'tcp';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conname = 'egress_flow_log_protocol_check'
                      AND conrelid = 'egress_flow_log'::regclass) THEN
        ALTER TABLE egress_flow_log
            ADD CONSTRAINT egress_flow_log_protocol_check CHECK (protocol IN ('tcp', 'udp')) NOT VALID;
    END IF;
END
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE egress_flow_log DROP CONSTRAINT IF EXISTS egress_flow_log_protocol_check;
ALTER TABLE egress_flow_log DROP COLUMN IF EXISTS protocol;
ALTER TABLE egress_flow_log DROP COLUMN IF EXISTS blocked;
-- +goose StatementEnd
