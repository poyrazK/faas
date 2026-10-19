-- filename: 20261016175839311_ci_github_automation.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS ci_automation_policies (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 binding_id uuid NOT NULL,
 binding_generation bigint NOT NULL CHECK(binding_generation>0),
 profile_id uuid NOT NULL,
 push_branches jsonb NOT NULL CHECK(jsonb_typeof(push_branches)='array' AND jsonb_array_length(push_branches) BETWEEN 1 AND 16),
 enabled boolean NOT NULL DEFAULT true,
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,profile_id) REFERENCES ci_execution_profiles(account_id,id) ON DELETE CASCADE,
 UNIQUE(account_id,binding_id),
 UNIQUE(account_id,id)
);

CREATE TABLE IF NOT EXISTS ci_automation_events (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 binding_id uuid NOT NULL,
 policy_id uuid NOT NULL,
 policy_generation bigint NOT NULL CHECK(policy_generation>0),
 profile_id uuid NOT NULL,
 event_key text NOT NULL CHECK(event_key ~ '^[0-9a-f]{64}$'),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=65536),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','admitted','ignored','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 8),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled')),
 execution_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,policy_id) REFERENCES ci_automation_policies(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,profile_id) REFERENCES ci_execution_profiles(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,execution_id) REFERENCES ci_executions(account_id,id),
 UNIQUE(account_id,binding_id,event_key),
 UNIQUE(account_id,execution_id),
 CHECK((status='processing' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
    OR (status<>'processing' AND lease_token IS NULL AND lease_until IS NULL)),
 CHECK((status='admitted' AND execution_id IS NOT NULL) OR (status<>'admitted' AND execution_id IS NULL))
);
CREATE INDEX IF NOT EXISTS ci_automation_events_due_idx ON ci_automation_events(account_id,next_attempt_at,created_at,id)
 WHERE status IN ('pending','processing');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_automation_events) THEN
  RAISE EXCEPTION 'retain and reconcile the CI automation deduplication journal before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_automation_events;
DROP TABLE ci_automation_policies;
