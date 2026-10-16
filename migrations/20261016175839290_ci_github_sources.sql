-- +goose Up
CREATE TABLE IF NOT EXISTS ci_github_bindings (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,39}$'),
 revision_id uuid NOT NULL,
 installation_id bigint NOT NULL CHECK (installation_id > 0),
 repository_id bigint NOT NULL CHECK (repository_id > 0),
 repository text NOT NULL CHECK (octet_length(repository) BETWEEN 3 AND 200),
 push_branches jsonb NOT NULL CHECK (jsonb_typeof(push_branches) = 'array' AND jsonb_array_length(push_branches) <= 16),
 pr_branches jsonb NOT NULL CHECK (jsonb_typeof(pr_branches) = 'array' AND jsonb_array_length(pr_branches) <= 16),
 enabled boolean NOT NULL DEFAULT true,
 generation bigint NOT NULL CHECK (generation > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (account_id, revision_id) REFERENCES ci_pipeline_revisions(account_id,id) ON DELETE CASCADE,
 UNIQUE (account_id,name),
 UNIQUE (account_id,id),
 CHECK (jsonb_array_length(push_branches) + jsonb_array_length(pr_branches) > 0)
);

ALTER TABLE ci_runs ADD COLUMN IF NOT EXISTS github_source jsonb
 CHECK (github_source IS NULL OR (jsonb_typeof(github_source) = 'object' AND octet_length(github_source::text) <= 65536));

-- githubd writes authenticated, compact inbox snapshots. apid alone creates runs.
CREATE TABLE IF NOT EXISTS ci_github_triggers (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 binding_id uuid NOT NULL,
 event_key text NOT NULL CHECK (event_key ~ '^[0-9a-f]{64}$'),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object' AND octet_length(snapshot::text) <= 65536),
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','admitted','ignored','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('','unavailable','admission_failed','access_denied','binding_disabled')),
 run_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY (account_id,run_id) REFERENCES ci_runs(account_id,id) ON DELETE CASCADE,
 UNIQUE (account_id,binding_id,event_key),
 UNIQUE (account_id,run_id),
 CHECK ((status = 'processing' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
     OR (status <> 'processing' AND lease_token IS NULL AND lease_until IS NULL)),
 CHECK ((status = 'admitted' AND run_id IS NOT NULL) OR (status <> 'admitted' AND run_id IS NULL))
);
CREATE INDEX IF NOT EXISTS ci_github_triggers_due_idx ON ci_github_triggers(account_id,next_attempt_at,created_at) WHERE status IN ('pending','processing');

CREATE TABLE IF NOT EXISTS ci_github_checks (
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sending','sent','ignored','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
 check_id bigint NOT NULL DEFAULT 0 CHECK (check_id >= 0),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('','unavailable','publish_failed','access_denied','binding_disabled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (account_id,run_id),
 FOREIGN KEY (account_id,run_id) REFERENCES ci_runs(account_id,id) ON DELETE CASCADE,
 CHECK ((status = 'sending' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
     OR (status <> 'sending' AND lease_token IS NULL AND lease_until IS NULL)),
 CHECK (status <> 'sent' OR check_id > 0)
);
CREATE INDEX IF NOT EXISTS ci_github_checks_due_idx ON ci_github_checks(account_id,next_attempt_at,created_at) WHERE status IN ('pending','sending');

-- +goose Down
DROP TABLE ci_github_checks;
DROP TABLE ci_github_triggers;
ALTER TABLE ci_runs DROP COLUMN github_source;
DROP TABLE ci_github_bindings;
