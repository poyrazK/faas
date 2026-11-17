-- filename: 20261117091254676_sandbox_egress_policies.sql
-- migration-compat: expand

-- +goose Up
-- The new table's foreign key locks accounts briefly; the sandbox_sessions
-- columns lock it briefly, rewrite no rows, and their CHECKs are NOT VALID,
-- so the lock never scans the table.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261117091026 §2: an account's default web-mode domain policy,
-- written by apid. No row is the empty policy.
CREATE TABLE IF NOT EXISTS sandbox_egress_policies (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    allow_rules text[] NOT NULL DEFAULT '{}' CHECK (cardinality(allow_rules) <= 100),
    deny_rules  text[] NOT NULL DEFAULT '{}' CHECK (cardinality(deny_rules) <= 100),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE sandbox_egress_policies IS
    'ADR-20261117091026 account default web-mode domain policy, written by apid.';

-- The policy a session was admitted under, fixed at admission. NULL is no
-- policy, so older binaries keep working.
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS egress_allow text[],
    ADD COLUMN IF NOT EXISTS egress_deny text[];
DO $policy$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'sandbox_sessions'::regclass AND conname = 'sandbox_sessions_egress_allow_check') THEN
    ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_egress_allow_check
      CHECK (egress_allow IS NULL OR cardinality(egress_allow) <= 100) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'sandbox_sessions'::regclass AND conname = 'sandbox_sessions_egress_deny_check') THEN
    ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_egress_deny_check
      CHECK (egress_deny IS NULL OR cardinality(egress_deny) <= 200) NOT VALID;
  END IF;
END
$policy$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_sessions
    DROP COLUMN IF EXISTS egress_deny,
    DROP COLUMN IF EXISTS egress_allow;
DROP TABLE IF EXISTS sandbox_egress_policies;
-- +goose StatementEnd
