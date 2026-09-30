-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environments ADD CONSTRAINT project_environments_gitops_scope_uniq
    UNIQUE (account_id, project_id, id);

CREATE TABLE environment_git_sources (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL,
    project_id uuid NOT NULL,
    environment_id uuid NOT NULL UNIQUE,
    repository_id bigint NOT NULL CHECK (repository_id > 0),
    installation_id bigint NOT NULL CHECK (installation_id > 0),
    repository text NOT NULL CHECK (repository ~ '^[^/[:space:]]+/[^/[:space:]]+$'),
    source_ref text NOT NULL CHECK (source_ref <> '' AND source_ref !~ '[[:space:]]'),
    manifest_path text NOT NULL CHECK (manifest_path <> '' AND manifest_path !~ '^/' AND manifest_path !~ '(^|/)\.\.(/|$)' AND position(chr(92) in manifest_path) = 0),
    mode text NOT NULL CHECK (mode IN ('report', 'enforce')),
    approval_policy text NOT NULL CHECK (approval_policy IN ('manual', 'protected_branch')),
    prune boolean NOT NULL DEFAULT false,
    suspended boolean NOT NULL DEFAULT false,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    intent_version bigint NOT NULL DEFAULT 0 CHECK (intent_version >= 0),
    approved_revision_id uuid,
    applied_revision_id uuid,
    source_checked_at timestamptz,
    source_error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, project_id, environment_id)
        REFERENCES project_environments(account_id, project_id, id) ON DELETE CASCADE,
    UNIQUE (id, environment_id)
);

CREATE TABLE environment_desired_revisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    commit_sha text NOT NULL CHECK (commit_sha ~ '^([a-f0-9]{40}|[a-f0-9]{64})$'),
    definition_digest text NOT NULL CHECK (definition_digest ~ '^[a-f0-9]{64}$'),
    definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
    approved_by text NOT NULL CHECK (approved_by <> ''),
    approved_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, commit_sha, definition_digest),
    UNIQUE (source_id, id)
);

ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_sources_approved_revision_fk
    FOREIGN KEY (id, approved_revision_id) REFERENCES environment_desired_revisions(source_id, id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_sources_applied_revision_fk
    FOREIGN KEY (id, applied_revision_id) REFERENCES environment_desired_revisions(source_id, id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE environment_gitops_resources (
    source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    logical_name text NOT NULL CHECK (logical_name ~ '^workload/[a-z0-9][a-z0-9-]*$'),
    app_id uuid REFERENCES apps(id) ON DELETE SET NULL,
    created_by_source boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, logical_name),
    UNIQUE (source_id, app_id)
);

CREATE TABLE environment_managed_fields (
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    resource text NOT NULL CHECK (resource = 'environment' OR resource ~ '^workload/[a-z0-9][a-z0-9-]*$'),
    field_path text NOT NULL CHECK (field_path <> '' AND position('#' in field_path) = 0),
    manager_kind text NOT NULL CHECK (manager_kind IN ('git', 'terraform', 'operator')),
    manager_id text NOT NULL CHECK (manager_id <> ''),
    source_id uuid,
    desired_value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, resource, field_path),
    FOREIGN KEY (source_id, environment_id) REFERENCES environment_git_sources(id, environment_id) ON DELETE CASCADE,
    CHECK ((manager_kind = 'git' AND source_id IS NOT NULL AND manager_id = source_id::text)
        OR (manager_kind <> 'git' AND source_id IS NULL))
);

CREATE TABLE environment_management_overrides (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id uuid NOT NULL,
    resource text NOT NULL,
    field_path text NOT NULL,
    authorized_by text NOT NULL CHECK (authorized_by <> ''),
    reason text NOT NULL CHECK (reason <> ''),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (environment_id, resource, field_path)
        REFERENCES environment_managed_fields(environment_id, resource, field_path) ON DELETE CASCADE,
    UNIQUE (environment_id, resource, field_path),
    CHECK (expires_at > created_at)
);

-- One durable work item per source. Updating desired_generation immediately
-- fences a superseded lease; claims and every mutation validate both values.
CREATE TABLE environment_gitops_jobs (
    source_id uuid PRIMARY KEY REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    desired_generation bigint NOT NULL CHECK (desired_generation > 0),
    claimed_generation bigint NOT NULL DEFAULT 0 CHECK (claimed_generation >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_token text NOT NULL DEFAULT '',
    lease_until timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    CHECK ((lease_token = '' AND lease_until IS NULL) OR (lease_token <> '' AND lease_until IS NOT NULL))
);
CREATE INDEX environment_gitops_jobs_due_idx ON environment_gitops_jobs(next_attempt_at);

CREATE TABLE environment_gitops_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    revision_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    lease_token text NOT NULL CHECK (lease_token <> ''),
    status text NOT NULL CHECK (status IN ('planning', 'drifted', 'blocked', 'applying', 'partial', 'overridden', 'converged', 'superseded', 'failed')),
    plan jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(plan) = 'object'),
    steps jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(steps) = 'array'),
    error_code text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    FOREIGN KEY (source_id, revision_id) REFERENCES environment_desired_revisions(source_id, id)
);
CREATE INDEX environment_gitops_runs_source_history_idx ON environment_gitops_runs(source_id, started_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE environment_gitops_runs;
DROP TABLE environment_gitops_jobs;
DROP TABLE environment_management_overrides;
DROP TABLE environment_managed_fields;
DROP TABLE environment_gitops_resources;
ALTER TABLE environment_git_sources DROP CONSTRAINT environment_git_sources_approved_revision_fk;
ALTER TABLE environment_git_sources DROP CONSTRAINT environment_git_sources_applied_revision_fk;
DROP TABLE environment_desired_revisions;
DROP TABLE environment_git_sources;
ALTER TABLE project_environments DROP CONSTRAINT project_environments_gitops_scope_uniq;
-- +goose StatementEnd
