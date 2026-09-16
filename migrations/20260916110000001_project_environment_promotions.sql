-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_environment_promotions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id       uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    project_slug     text NOT NULL,
    from_environment text NOT NULL,
    to_environment   text NOT NULL,
    promotion_hash   text NOT NULL,
    idempotency_key  text NOT NULL,
    status           text NOT NULL DEFAULT 'running',
    error            text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    completed_at     timestamptz,
    CONSTRAINT project_environment_promotions_hash_shape CHECK (promotion_hash ~ '^[a-f0-9]{64}$'),
    CONSTRAINT project_environment_promotions_status_check CHECK (status IN ('running', 'succeeded', 'failed')),
    CONSTRAINT project_environment_promotions_project_slug_shape CHECK (project_slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    CONSTRAINT project_environment_promotions_from_slug_shape CHECK (from_environment ~ '^[a-z0-9](?:[a-z0-9-]{0,31}[a-z0-9])?$'),
    CONSTRAINT project_environment_promotions_to_slug_shape CHECK (to_environment ~ '^[a-z0-9](?:[a-z0-9-]{0,31}[a-z0-9])?$'),
    CONSTRAINT project_environment_promotions_key_shape CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    CONSTRAINT project_environment_promotions_environments_differ CHECK (from_environment <> to_environment),
    CONSTRAINT project_environment_promotions_key_uniq UNIQUE (account_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS project_environment_promotions_lookup_idx
    ON project_environment_promotions (account_id, project_slug, to_environment, created_at DESC);

CREATE TABLE IF NOT EXISTS project_environment_promotion_workloads (
    id                         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    promotion_id               uuid NOT NULL REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
    workload_slug              text NOT NULL,
    workload_name              text NOT NULL DEFAULT '',
    source_deployment_id       text NOT NULL DEFAULT '',
    previous_target_deployment_id text NOT NULL DEFAULT '',
    target_deployment_id       text NOT NULL DEFAULT '',
    status                     text NOT NULL DEFAULT 'pending',
    error                      text NOT NULL DEFAULT '',
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_promotion_workloads_status_check CHECK (status IN ('pending', 'promoted', 'unchanged', 'failed')),
    CONSTRAINT project_environment_promotion_workloads_unique UNIQUE (promotion_id, workload_slug)
);

CREATE INDEX IF NOT EXISTS project_environment_promotion_workloads_lookup_idx
    ON project_environment_promotion_workloads (promotion_id, created_at, id);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS project_environment_promotion_workloads;
DROP TABLE IF EXISTS project_environment_promotions;
