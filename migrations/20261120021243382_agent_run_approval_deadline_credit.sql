-- filename: 20261120021243382_agent_run_approval_deadline_credit.sql
-- migration-compat: expand

-- +goose Up
-- Nullable column and a constant default: catalog-only changes.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090016 amendment 3: a run's deadline pauses while a person
-- decides. outboundd records when it stopped holding an approved, denied,
-- expired or abandoned call; agentd records the credit a run may earn
-- (0: none, the amendment 2 deadline). Older writers leave both at their
-- defaults.
ALTER TABLE agent_run_approvals ADD COLUMN IF NOT EXISTS released_at timestamptz;
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS deadline_credit_max_sec integer NOT NULL DEFAULT 0;
-- NOT VALID: new rows are checked at once; existing rows all hold the
-- default 0, so a later VALIDATE has nothing to find.
DO $credit$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'agent_runs'::regclass
                    AND conname = 'agent_runs_deadline_credit_max_sec_check') THEN
    ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_deadline_credit_max_sec_check
      CHECK (deadline_credit_max_sec BETWEEN 0 AND 3600) NOT VALID;
  END IF;
END
$credit$;
COMMENT ON COLUMN agent_run_approvals.released_at IS
    'ADR-20261027090016 amendment 3: when outboundd stopped holding the call; null while held or from an older outboundd.';
COMMENT ON COLUMN agent_runs.deadline_credit_max_sec IS
    'ADR-20261027090016 amendment 3: approval-wait credit this run may add to its deadline, written by agentd at running.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_deadline_credit_max_sec_check;
ALTER TABLE agent_runs DROP COLUMN IF EXISTS deadline_credit_max_sec;
ALTER TABLE agent_run_approvals DROP COLUMN IF EXISTS released_at;
-- +goose StatementEnd
