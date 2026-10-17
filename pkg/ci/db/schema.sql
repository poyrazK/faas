-- Compile-time projection. Runtime ownership: migrations 20261016175839285
-- 20261016175839288, 20261016175839290, 20261016175839296, 20261016175839299,
-- and 20261016175839301.
CREATE TABLE accounts (
 id uuid PRIMARY KEY,
 plan text NOT NULL,
 status text NOT NULL,
 abuse_hold_at timestamptz
);

CREATE TABLE ci_pipeline_revisions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,39}$'),
 digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
 definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object' AND octet_length(definition::text) <= 131072),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (account_id, name, digest),
 UNIQUE (account_id, id)
);

CREATE TABLE ci_runs (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision_id uuid NOT NULL,
 pipeline text NOT NULL CHECK (pipeline ~ '^[a-z][a-z0-9-]{0,39}$'),
 commit_sha text NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
 idempotency_key text NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 128),
 plan jsonb NOT NULL CHECK (jsonb_typeof(plan) = 'object' AND octet_length(plan::text) <= 131072),
 status text NOT NULL CHECK (status IN ('blocked', 'cancelled')),
 blocked_reason text NOT NULL CHECK (blocked_reason IN ('compute_unavailable', '')),
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 FOREIGN KEY (account_id, revision_id) REFERENCES ci_pipeline_revisions(account_id, id) ON DELETE CASCADE,
 UNIQUE (account_id, idempotency_key),
 UNIQUE (account_id, id),
 CHECK ((status = 'blocked' AND blocked_reason = 'compute_unavailable' AND finished_at IS NULL)
     OR (status = 'cancelled' AND blocked_reason = '' AND finished_at IS NOT NULL))
);

CREATE TABLE ci_run_events (
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 sequence integer NOT NULL CHECK (sequence BETWEEN 1 AND 2),
 event_type text NOT NULL CHECK (event_type IN ('blocked', 'cancelled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (run_id, sequence),
 FOREIGN KEY (account_id, run_id) REFERENCES ci_runs(account_id, id) ON DELETE CASCADE,
 CHECK ((sequence = 1 AND event_type = 'blocked') OR (sequence = 2 AND event_type = 'cancelled'))
);

CREATE INDEX ci_runs_account_created_idx ON ci_runs(account_id, created_at DESC, id);
CREATE INDEX ci_revisions_account_created_idx ON ci_pipeline_revisions(account_id, created_at DESC, id);

-- Separate from metadata receipts: no conversion, trigger or daemon admission.
CREATE TABLE ci_executions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision_id uuid NOT NULL,
 commit_sha text NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
 idempotency_key text NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 128),
 mode text NOT NULL DEFAULT 'qualification' CHECK (mode IN ('qualification', 'jobs')),
 status text NOT NULL CHECK (status IN ('queued', 'running', 'cancelling', 'succeeded', 'failed', 'cancelled')),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object' AND octet_length(snapshot::text) <= 4194304),
 version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
 fence bigint NOT NULL DEFAULT 0 CHECK (fence >= 0),
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 FOREIGN KEY (account_id, revision_id) REFERENCES ci_pipeline_revisions(account_id, id) ON DELETE CASCADE,
 UNIQUE (account_id, idempotency_key),
 UNIQUE (account_id, id),
 CHECK (snapshot->>'status' IS NOT NULL AND status = snapshot->>'status'),
 CHECK (snapshot->>'mode' IS NOT NULL AND mode = snapshot->>'mode'),
 CHECK ((mode = 'jobs' AND snapshot->'compute' IS NOT NULL AND jsonb_typeof(snapshot->'compute') = 'object')
     OR (mode = 'qualification' AND (snapshot->'compute' IS NULL OR snapshot->'compute' = 'null'::jsonb))),
 CHECK ((status IN ('queued', 'running', 'cancelling') AND finished_at IS NULL)
     OR (status IN ('succeeded', 'failed', 'cancelled') AND finished_at IS NOT NULL AND lease_until IS NULL))
);
CREATE INDEX ci_executions_account_created_idx ON ci_executions(account_id, created_at DESC, id);


CREATE TABLE ci_github_bindings (
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

ALTER TABLE ci_runs ADD COLUMN github_source jsonb
 CHECK (github_source IS NULL OR (jsonb_typeof(github_source) = 'object' AND octet_length(github_source::text) <= 65536));

-- githubd writes authenticated, compact inbox snapshots. apid alone creates runs.
CREATE TABLE ci_github_triggers (
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
CREATE INDEX ci_github_triggers_due_idx ON ci_github_triggers(account_id,next_attempt_at,created_at) WHERE status IN ('pending','processing');

CREATE TABLE ci_github_checks (
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
CREATE INDEX ci_github_checks_due_idx ON ci_github_checks(account_id,next_attempt_at,created_at) WHERE status IN ('pending','sending');


-- Existing Jobs projections; apid writes intent, schedd owns task/instance lifecycle.
CREATE TABLE jobs (
 id uuid PRIMARY KEY, account_id uuid NOT NULL, kind text NOT NULL, image_ref text NOT NULL,
 ram_mb integer NOT NULL, task_timeout_s integer NOT NULL, max_parallelism integer NOT NULL,
 retry_max integer NOT NULL, env_overrides jsonb NOT NULL, status text NOT NULL,
 command text[] NOT NULL, image_resolved_digest text, image_storage_key text,
 image_materialization_status text NOT NULL, failure_rules jsonb
);
CREATE TABLE job_runs (
 id uuid PRIMARY KEY, job_id uuid NOT NULL, account_id uuid NOT NULL,
 trigger_kind text NOT NULL, tasks integer NOT NULL, parallelism integer NOT NULL,
 retry_max integer, task_timeout_s integer, command text[],
 image_ref_snapshot text, image_resolved_digest_snapshot text, image_storage_key_snapshot text,
 ram_mb_snapshot integer, effective_env_snapshot jsonb, env_overrides jsonb, failure_rules jsonb,
 execution_class text NOT NULL DEFAULT 'standard',
 aggregate_status text NOT NULL DEFAULT 'queued', finished_at timestamptz,
 tasks_succeeded integer NOT NULL DEFAULT 0, tasks_failed integer NOT NULL DEFAULT 0,
 tasks_cancelled integer NOT NULL DEFAULT 0, tasks_running integer NOT NULL DEFAULT 0,
 start_deadline_at timestamptz
);
CREATE TABLE job_tasks (
 run_id uuid NOT NULL, task_index integer NOT NULL, status text NOT NULL DEFAULT 'queued',
 attempt integer NOT NULL DEFAULT 1, instance_id uuid, exit_code integer, finished_at timestamptz,
 lease_token uuid, lease_expires_at timestamptz, last_lease_node text,
 PRIMARY KEY (run_id, task_index)
);
CREATE TABLE instances (id uuid PRIMARY KEY, state text NOT NULL, kind text NOT NULL, job_id uuid, ram_mb integer NOT NULL);

CREATE TABLE ci_job_attempts (
 attempt_id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 execution_id uuid NOT NULL,
 job_name text NOT NULL CHECK (job_name ~ '^[a-z][a-z0-9-]{0,39}$'),
 fence bigint NOT NULL CHECK (fence > 0),
 cancel_requested boolean NOT NULL DEFAULT false,
 run_id uuid UNIQUE REFERENCES job_runs(id),
 source jsonb CHECK (source IS NULL OR (jsonb_typeof(source) = 'object' AND octet_length(source::text) <= 16384)),
 output jsonb CHECK (output IS NULL OR (jsonb_typeof(output) = 'object' AND octet_length(output::text) <= 262144)),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (account_id, execution_id) REFERENCES ci_executions(account_id, id) ON DELETE CASCADE,
 UNIQUE (execution_id, job_name),
 CHECK ((run_id IS NULL AND source IS NULL) OR (run_id IS NOT NULL AND source IS NOT NULL)),
 CHECK (run_id IS NULL OR run_id = attempt_id),
 CHECK (output IS NULL OR run_id IS NOT NULL)
);
CREATE INDEX ci_job_attempts_account_execution_idx ON ci_job_attempts(account_id, execution_id);


-- Runtime grants are issued only through a host lease proof; never through HTTP.
CREATE TABLE ci_job_runtime (
 attempt_id uuid PRIMARY KEY REFERENCES ci_job_attempts(attempt_id) ON DELETE CASCADE,
 instance_id uuid NOT NULL REFERENCES instances(id),
 capability_hash bytea NOT NULL CHECK (octet_length(capability_hash) = 32),
 expires_at timestamptz NOT NULL,
 cancelled_at timestamptz,
 result jsonb CHECK (result IS NULL OR (jsonb_typeof(result) = 'object' AND octet_length(result::text) <= 32768)),
 sealed_at timestamptz,
 transfer_token uuid,
 transfer_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((result IS NULL) = (sealed_at IS NULL)),
 CHECK ((transfer_token IS NULL) = (transfer_until IS NULL)),
 CHECK (expires_at > created_at)
);
CREATE TABLE ci_job_runtime_objects (
 attempt_id uuid NOT NULL REFERENCES ci_job_runtime(attempt_id) ON DELETE CASCADE,
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 512),
 digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
 bytes bigint NOT NULL CHECK (bytes BETWEEN 0 AND 33554432),
 retained boolean NOT NULL DEFAULT false,
 PRIMARY KEY (attempt_id, name)
);


-- One short global lock serializes CI storage reservations, never blob I/O.
CREATE TABLE ci_storage_budget (lock_id boolean PRIMARY KEY CHECK(lock_id));
INSERT INTO ci_storage_budget VALUES(true);

-- Deliberately survives account/execution deletion so orphan payloads retain
-- recovery authority. Physical object IDs are immutable and never reused.
CREATE TABLE ci_managed_objects (
 object_key text PRIMARY KEY CHECK(octet_length(object_key) BETWEEN 1 AND 1024),
 id uuid NOT NULL UNIQUE,
 account_id uuid NOT NULL,
 execution_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 job_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('source','logs','artifact')),
 digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 bytes bigint NOT NULL CHECK(bytes BETWEEN 0 AND 33554432),
 state text NOT NULL CHECK(state IN ('pending','ready','deleting','deleted')),
 op_token uuid,
 op_until timestamptz,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 ready_at timestamptz,
 deleted_at timestamptz,
 CHECK((op_token IS NULL) = (op_until IS NULL)),
 CHECK(expires_at > created_at),
 CHECK(state!='ready' OR ready_at IS NOT NULL),
 CHECK((state='deleted') = (deleted_at IS NOT NULL))
);
CREATE INDEX ci_managed_objects_account_idx ON ci_managed_objects(account_id,state);
CREATE INDEX ci_managed_objects_attempt_idx ON ci_managed_objects(attempt_id);
CREATE INDEX ci_managed_objects_retention_idx ON ci_managed_objects(state,expires_at,created_at);


-- ADR-1339: independent dispatcher accounting lock and durable scheduling.
CREATE TABLE ci_dispatch_budget (lock_id boolean PRIMARY KEY CHECK(lock_id));
ALTER TABLE ci_executions ADD COLUMN next_reconcile_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN reconcile_failures integer NOT NULL DEFAULT 0 CHECK(reconcile_failures BETWEEN 0 AND 16);

-- Saved internal intent only. No trigger, automatic admission or flag enablement.
CREATE TABLE ci_execution_profiles (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(name ~ '^[a-z][a-z0-9-]{0,39}$'),
 revision_id uuid NOT NULL,
 revision_digest text NOT NULL CHECK(revision_digest ~ '^sha256:[0-9a-f]{64}$'),
 binding_id uuid NOT NULL,
 binding_generation bigint NOT NULL CHECK(binding_generation > 0),
 templates jsonb NOT NULL CHECK(jsonb_typeof(templates)='object' AND octet_length(templates::text)<=65536),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=65536),
 state text NOT NULL DEFAULT 'active' CHECK(state IN ('active','disabled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,revision_id) REFERENCES ci_pipeline_revisions(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE,
 UNIQUE(account_id,name),
 UNIQUE(account_id,id)
);
CREATE INDEX ci_execution_profiles_account_created_idx ON ci_execution_profiles(account_id,created_at DESC,id);

CREATE TABLE ci_automation_policies (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 binding_id uuid NOT NULL,
 binding_generation bigint NOT NULL CHECK(binding_generation>0),
 profile_id uuid NOT NULL,
 push_branches jsonb NOT NULL CHECK(jsonb_typeof(push_branches)='array' AND jsonb_array_length(push_branches) BETWEEN 0 AND 16),
 pr_branches jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(pr_branches)='array' AND jsonb_array_length(pr_branches) BETWEEN 0 AND 16),
 latest_only boolean NOT NULL DEFAULT false,
 enabled boolean NOT NULL DEFAULT true,
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,profile_id) REFERENCES ci_execution_profiles(account_id,id) ON DELETE CASCADE,
 UNIQUE(account_id,binding_id),
 UNIQUE(account_id,id),
 CHECK(jsonb_array_length(push_branches)+jsonb_array_length(pr_branches)>0)
);

CREATE TABLE ci_automation_events (
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
 retry_generation integer NOT NULL DEFAULT 0 CHECK(retry_generation BETWEEN 0 AND 3),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled','branch_wait','head_changed','branch_deleted','pr_wait','pr_closed','pr_draft','pr_changed')),
 execution_id uuid,
 superseded_by uuid,
 superseded_sha text,
 superseded_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,policy_id) REFERENCES ci_automation_policies(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,profile_id) REFERENCES ci_execution_profiles(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,execution_id) REFERENCES ci_executions(account_id,id),
 UNIQUE(account_id,id),
 UNIQUE(account_id,binding_id,event_key),
 UNIQUE(account_id,execution_id),
 FOREIGN KEY(account_id,superseded_by) REFERENCES ci_automation_events(account_id,id),
 CHECK((superseded_sha IS NULL AND superseded_at IS NULL AND superseded_by IS NULL)
 OR (superseded_sha IS NOT NULL AND superseded_sha ~ '^[0-9a-f]{40}$' AND superseded_at IS NOT NULL AND (superseded_by IS NULL OR superseded_by<>id))),
 CHECK((status='processing' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
    OR (status<>'processing' AND lease_token IS NULL AND lease_until IS NULL)),
 CHECK((status='admitted' AND execution_id IS NOT NULL) OR (status<>'admitted' AND execution_id IS NULL))
);
CREATE INDEX ci_automation_events_due_idx ON ci_automation_events(account_id,next_attempt_at,created_at,id)
 WHERE status IN ('pending','processing');

CREATE TABLE ci_automation_branches (
 account_id uuid NOT NULL,
 binding_id uuid NOT NULL,
 branch text NOT NULL CHECK(octet_length(branch) BETWEEN 1 AND 255),
 head_fence bigint NOT NULL CHECK(head_fence>0),
 PRIMARY KEY(account_id,binding_id,branch),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE
);

CREATE TABLE ci_automation_pull_requests (
 account_id uuid NOT NULL,
 binding_id uuid NOT NULL,
 pr_number bigint NOT NULL CHECK(pr_number>0),
 head_fence bigint NOT NULL CHECK(head_fence>0),
 PRIMARY KEY(account_id,binding_id,pr_number),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE
);

CREATE TABLE ci_automation_event_retries (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 event_id uuid NOT NULL,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 3),
 key_digest text NOT NULL CHECK(key_digest ~ '^[0-9a-f]{64}$'),
 previous_attempts integer NOT NULL CHECK(previous_attempts=8),
 previous_error text NOT NULL CHECK(previous_error IN ('unavailable','capacity')),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(account_id,event_id) REFERENCES ci_automation_events(account_id,id) ON DELETE CASCADE,
 UNIQUE(account_id,key_digest),
 UNIQUE(event_id,generation)
);


-- githubd owns delivery bookkeeping. apid owns execution versions and intent.
CREATE TABLE ci_execution_checks (
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
CREATE INDEX ci_execution_checks_due_idx ON ci_execution_checks(account_id,next_attempt_at,created_at,execution_id)
 WHERE status IN ('pending','sending','sent');

-- apid admits immutable retry intent; githubd applies it to delivery bookkeeping.
ALTER TABLE ci_execution_checks ADD COLUMN retry_generation integer NOT NULL DEFAULT 0
 CHECK(retry_generation BETWEEN 0 AND 3);
ALTER TABLE ci_execution_checks ADD CONSTRAINT ci_execution_checks_account_execution_key UNIQUE(account_id,execution_id);
CREATE TABLE ci_execution_check_retries (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 execution_id uuid NOT NULL,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 3),
 key_digest text NOT NULL CHECK(key_digest ~ '^[0-9a-f]{64}$'),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 applied_at timestamptz,
 FOREIGN KEY(account_id,execution_id) REFERENCES ci_execution_checks(account_id,execution_id) ON DELETE CASCADE,
 UNIQUE(account_id,key_digest),
 UNIQUE(execution_id,generation),
 CHECK(applied_at IS NULL OR applied_at>=requested_at)
);

-- Keep parent identity and foreign keys; only detailed Jobs history is compacted.
ALTER TABLE ci_executions ADD COLUMN archived_at timestamptz;
ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_archive_valid CHECK(archived_at IS NULL OR
 (mode='jobs' AND status IN ('succeeded','failed','cancelled') AND finished_at IS NOT NULL AND lease_until IS NULL
 AND octet_length(snapshot::text)<=1024));
CREATE TABLE ci_execution_archives (
 account_id uuid NOT NULL,
 execution_id uuid PRIMARY KEY,
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384),
 archived_at timestamptz NOT NULL,
 FOREIGN KEY(account_id,execution_id) REFERENCES ci_executions(account_id,id) ON DELETE CASCADE,
 CHECK(receipt->>'execution_id' IS NOT NULL AND receipt->>'execution_id'=execution_id::text)
);
CREATE INDEX ci_execution_archives_account_idx ON ci_execution_archives(account_id,archived_at DESC,execution_id);

-- Fixed diagnostic slots, never a work/admission lease. No host or account labels.
CREATE TABLE ci_worker_health (
 worker text NOT NULL CHECK(worker IN ('dispatcher','metadata_admission','automation_admission','metadata_checks','execution_checks','retention')),
 slot integer NOT NULL CHECK(slot BETWEEN 0 AND 7),
 token uuid NOT NULL,
 enabled boolean NOT NULL,
 ready boolean NOT NULL,
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 cycle_at timestamptz,
 cycle_sequence bigint NOT NULL DEFAULT 0 CHECK(cycle_sequence>=0),
 success_at timestamptz,
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','unavailable')),
 PRIMARY KEY(worker,slot),
 CHECK(success_at IS NULL OR cycle_at IS NOT NULL)
);

-- ADR-1584: explicit whole-pipeline rerun lineage.
ALTER TABLE ci_executions ADD COLUMN IF NOT EXISTS rerun_parent_id uuid;
ALTER TABLE ci_executions ADD COLUMN IF NOT EXISTS rerun_root_id uuid;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_executions'::regclass AND conname='ci_execution_rerun_parent_fk') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_rerun_parent_fk FOREIGN KEY(account_id,rerun_parent_id)
   REFERENCES ci_executions(account_id,id) ON DELETE CASCADE;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_executions'::regclass AND conname='ci_execution_rerun_root_fk') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_rerun_root_fk FOREIGN KEY(account_id,rerun_root_id)
   REFERENCES ci_executions(account_id,id) ON DELETE CASCADE;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_executions'::regclass AND conname='ci_execution_rerun_identity') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_rerun_identity CHECK(
   (rerun_parent_id IS NULL AND rerun_root_id IS NULL AND (snapshot->'rerun' IS NULL OR snapshot->'rerun'='null'::jsonb)) OR
   (rerun_parent_id IS NOT NULL AND rerun_root_id IS NOT NULL AND mode='jobs'
    AND id<>rerun_parent_id AND id<>rerun_root_id AND jsonb_typeof(snapshot->'rerun')='object'
    AND snapshot->'rerun'->>'parent_execution_id' IS NOT NULL AND snapshot->'rerun'->>'root_execution_id' IS NOT NULL
    AND snapshot->'rerun'->>'parent_execution_id'=rerun_parent_id::text AND snapshot->'rerun'->>'root_execution_id'=rerun_root_id::text));
 END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE ci_automation_policies ADD COLUMN IF NOT EXISTS include_paths jsonb NOT NULL DEFAULT '[]'
 CHECK(jsonb_typeof(include_paths)='array' AND jsonb_array_length(include_paths) BETWEEN 0 AND 16);
ALTER TABLE ci_automation_policies ADD COLUMN IF NOT EXISTS exclude_paths jsonb NOT NULL DEFAULT '[]'
 CHECK(jsonb_typeof(exclude_paths)='array' AND jsonb_array_length(exclude_paths) BETWEEN 0 AND 16);
ALTER TABLE ci_automation_events ADD COLUMN IF NOT EXISTS path_decision jsonb
 CHECK(path_decision IS NULL OR (jsonb_typeof(path_decision)='object' AND octet_length(path_decision::text)<=2048
 AND path_decision->>'head_sha' IS NOT NULL AND path_decision->>'head_sha'=snapshot->'source'->>'commit_sha'
 AND path_decision->>'outcome' IS NOT NULL AND path_decision->>'reason' IS NOT NULL
 AND ((path_decision->>'outcome'='run' AND path_decision->>'reason' IN ('paths_matched','changes_unavailable','prior_source_unsettled'))
 OR (path_decision->>'outcome'='skip' AND path_decision->>'reason'='paths_filtered' AND status='ignored' AND last_error='paths_filtered'))));
ALTER TABLE ci_automation_events DROP CONSTRAINT IF EXISTS ci_automation_events_last_error_check;
ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_last_error_check CHECK(last_error IN
 ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled',
  'branch_wait','head_changed','branch_deleted','pr_wait','pr_closed','pr_draft','pr_changed','paths_filtered'));
-- One immutable projection per retained skipped event; no execution or native work.
CREATE TABLE IF NOT EXISTS ci_automation_path_checks (
 account_id uuid NOT NULL,
 event_id uuid PRIMARY KEY,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=2048),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','ignored','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 8),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 check_id bigint NOT NULL DEFAULT 0 CHECK(check_id>=0),
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','publish_failed','access_denied','unavailable','attempts_exhausted')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,event_id) REFERENCES ci_automation_events(account_id,id) ON DELETE CASCADE,
 CHECK((status='sending' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
 OR (status<>'sending' AND lease_token IS NULL AND lease_until IS NULL)),
 CHECK(status<>'sent' OR check_id>0)
);
CREATE INDEX IF NOT EXISTS ci_automation_path_checks_due_idx ON ci_automation_path_checks(account_id,next_attempt_at,created_at,event_id)
 WHERE status IN ('pending','sending');

ALTER TABLE ci_automation_path_checks ADD COLUMN IF NOT EXISTS retry_generation integer NOT NULL DEFAULT 0
 CHECK(retry_generation BETWEEN 0 AND 3);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_automation_path_checks'::regclass AND conname='ci_automation_path_checks_account_event_key') THEN
  ALTER TABLE ci_automation_path_checks ADD CONSTRAINT ci_automation_path_checks_account_event_key UNIQUE(account_id,event_id);
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS ci_source_check_retries (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 event_id uuid NOT NULL,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 3),
 key_digest text NOT NULL CHECK(key_digest ~ '^[0-9a-f]{64}$'),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 applied_at timestamptz,
 FOREIGN KEY(account_id,event_id) REFERENCES ci_automation_path_checks(account_id,event_id) ON DELETE CASCADE,
 UNIQUE(account_id,key_digest),
 UNIQUE(event_id,generation),
 CHECK(applied_at IS NULL OR applied_at>=requested_at)
);

ALTER TABLE ci_job_attempts ADD COLUMN IF NOT EXISTS progress jsonb
 CHECK(progress IS NULL OR (jsonb_typeof(progress)='object' AND octet_length(progress::text)<=131072));
ALTER TABLE ci_job_attempts ADD COLUMN IF NOT EXISTS progress_sequence integer NOT NULL DEFAULT 0
 CHECK(progress_sequence BETWEEN 0 AND 4096);
ALTER TABLE ci_job_attempts ADD COLUMN IF NOT EXISTS progress_digest text NOT NULL DEFAULT ''
 CHECK(progress_digest='' OR progress_digest ~ '^[0-9a-f]{64}$');
