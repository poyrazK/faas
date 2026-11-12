-- +goose Up
-- +goose StatementBegin

-- ADR-20261112120603: a Free instance recycled for sustained CPU abuse counts
-- toward the ADR-361 account abuse hold, recorded with its own reason.
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_abuse_hold_valid;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_abuse_hold_valid
    CHECK (
        (abuse_hold_at IS NULL) = (abuse_hold_reason IS NULL)
        AND (abuse_hold_reason IS NULL OR abuse_hold_reason IN ('egress_fanout', 'egress_flood', 'cpu_sustained', 'operator'))
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE accounts SET abuse_hold_reason = 'operator' WHERE abuse_hold_reason = 'cpu_sustained';
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_abuse_hold_valid;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_abuse_hold_valid
    CHECK (
        (abuse_hold_at IS NULL) = (abuse_hold_reason IS NULL)
        AND (abuse_hold_reason IS NULL OR abuse_hold_reason IN ('egress_fanout', 'egress_flood', 'operator'))
    );
-- +goose StatementEnd
