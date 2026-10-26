-- filename: 20261026120843000_account_spend_cap_holds.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Spend cap holds (ADR-20261026120843). The billable overage a cap limits
-- includes every live provider meter (compute and, when live, egress), but
-- only meterd knows the provider's meter policies. meterd records an account
-- whose combined overage this UTC month has reached its cap; schedd's wake,
-- park and job gates honour the row while its month and cap still match.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS account_spend_cap_holds (
    account_id     uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    month          date NOT NULL CHECK (month = date_trunc('month', month)::date),
    cap_cents      bigint NOT NULL CHECK (cap_cents >= 0),
    observed_cents bigint NOT NULL CHECK (observed_cents >= cap_cents),
    reached_at     timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

COMMENT ON TABLE account_spend_cap_holds IS
    'Accounts whose combined billable overage (compute plus live secondary meters) reached overage_cap_cents this UTC month. Written by meterd, read by schedd (ADR-20261026120843).';

-- +goose Down
DROP TABLE IF EXISTS account_spend_cap_holds;
