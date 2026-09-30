-- +goose Up
-- +goose StatementBegin
-- Intent and its required serving-fleet effects commit together. The worker
-- may replay an older effect under a newer lease: invalidation loads current
-- intent, while the unique gateway generation releases only its own fence.
CREATE TABLE environment_gitops_effects (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    revision_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    intent_version bigint NOT NULL CHECK (intent_version >= 0),
    plan_hash text NOT NULL CHECK (plan_hash ~ '^[a-f0-9]{64}$'),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('edge_policy')),
    gateway_generation bigint NOT NULL UNIQUE CHECK (gateway_generation > 0),
    match_hosts text[] NOT NULL CHECK (cardinality(match_hosts) > 0 AND array_position(match_hosts, NULL) IS NULL),
    expected_nodes text[] NOT NULL CHECK (array_position(expected_nodes, NULL) IS NULL),
    acknowledged_nodes text[] NOT NULL DEFAULT '{}' CHECK (array_position(acknowledged_nodes, NULL) IS NULL),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    FOREIGN KEY (source_id, revision_id) REFERENCES environment_desired_revisions(source_id, id),
    CHECK (acknowledged_nodes <@ expected_nodes),
    CHECK (completed_at IS NULL OR expected_nodes <@ acknowledged_nodes),
    UNIQUE (source_id, generation, plan_hash, app_id, kind)
);
CREATE INDEX environment_gitops_effects_pending_idx ON environment_gitops_effects(source_id, gateway_generation) WHERE completed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
DROP TABLE environment_gitops_effects;
