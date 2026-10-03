-- +goose Up
-- +goose StatementBegin

-- ADR-361 decision 9: per-destination flood recycles escalate to the account
-- abuse hold too, recorded with their own reason.
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_abuse_hold_valid;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_abuse_hold_valid
    CHECK (
        (abuse_hold_at IS NULL) = (abuse_hold_reason IS NULL)
        AND (abuse_hold_reason IS NULL OR abuse_hold_reason IN ('egress_fanout', 'egress_flood', 'operator'))
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE accounts SET abuse_hold_reason = 'egress_fanout' WHERE abuse_hold_reason = 'egress_flood';
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_abuse_hold_valid;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_abuse_hold_valid
    CHECK (
        (abuse_hold_at IS NULL) = (abuse_hold_reason IS NULL)
        AND (abuse_hold_reason IS NULL OR abuse_hold_reason IN ('egress_fanout', 'operator'))
    );
-- +goose StatementEnd
