-- +goose Up
-- githubd owns delivery bookkeeping. apid owns execution versions and intent.
CREATE TABLE IF NOT EXISTS ci_execution_checks (
 account_id uuid NOT NULL,
 execution_id uuid PRIMARY KEY,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','ignored','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 8),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 delivered_version bigint NOT NULL DEFAULT 0 CHECK(delivered_version >= 0),
 delivered_digest text NOT NULL DEFAULT '' CHECK(delivered_digest = '' OR delivered_digest ~ '^[0-9a-f]{64}$'),
 check_id bigint NOT NULL DEFAULT 0 CHECK(check_id >= 0),
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','publish_failed','access_denied','unavailable','attempts_exhausted')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,execution_id) REFERENCES ci_executions(account_id,id) ON DELETE CASCADE,
 CHECK((status='sending' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
    OR (status<>'sending' AND lease_token IS NULL AND lease_until IS NULL)),
 CHECK((delivered_version=0 AND delivered_digest='' AND check_id=0)
    OR (delivered_version>0 AND delivered_digest<>'' AND check_id>0))
);
CREATE INDEX IF NOT EXISTS ci_execution_checks_due_idx ON ci_execution_checks(account_id,next_attempt_at,created_at,execution_id)
 WHERE status IN ('pending','sending','sent');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_execution_checks) THEN
  RAISE EXCEPTION 'retain the CI execution Check delivery journal before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_execution_checks;
