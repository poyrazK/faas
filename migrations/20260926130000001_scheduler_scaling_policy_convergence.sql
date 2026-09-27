-- +goose Up
-- +goose StatementBegin

-- Scaling policies are read from apps by schedd's periodic controllers. Keep
-- a desired revision on the app row and an observation watermark so runtime
-- status can distinguish a live policy from a scheduler that has not yet
-- loaded the latest row. The revision is deliberately independent of deploys.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS scaling_policy_revision bigint NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'apps_scaling_policy_revision_positive'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_scaling_policy_revision_positive
            CHECK (scaling_policy_revision > 0);
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION apps_bump_scaling_policy_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.min_instances,
        NEW.max_concurrency,
        NEW.idle_timeout_s,
        NEW.autoscale_target_rps,
        NEW.autoscale_target_cpu_pct,
        NEW.scaling_policy,
        NEW.workload_class,
        NEW.node_id
    ) IS DISTINCT FROM ROW(
        OLD.min_instances,
        OLD.max_concurrency,
        OLD.idle_timeout_s,
        OLD.autoscale_target_rps,
        OLD.autoscale_target_cpu_pct,
        OLD.scaling_policy,
        OLD.workload_class,
        OLD.node_id
    ) THEN
        NEW.scaling_policy_revision := OLD.scaling_policy_revision + 1;
    ELSE
        -- Callers cannot forge a revision by setting the bookkeeping column.
        NEW.scaling_policy_revision := OLD.scaling_policy_revision;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_bump_scaling_policy_revision_trg ON apps;
CREATE TRIGGER apps_bump_scaling_policy_revision_trg
    BEFORE UPDATE OF min_instances, max_concurrency, idle_timeout_s,
                     autoscale_target_rps, autoscale_target_cpu_pct,
                     scaling_policy, workload_class, node_id, scaling_policy_revision ON apps
    FOR EACH ROW EXECUTE FUNCTION apps_bump_scaling_policy_revision();

CREATE TABLE IF NOT EXISTS app_scaling_policy_scheduler_status (
    app_id                  uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    scheduler_node_id       uuid REFERENCES compute_nodes(id) ON DELETE SET NULL,
    observed_revision       bigint NOT NULL CHECK (observed_revision >= 0),
    observed_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS app_scaling_policy_scheduler_status_observed_idx
    ON app_scaling_policy_scheduler_status (observed_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_scaling_policy_scheduler_status;
DROP TRIGGER IF EXISTS apps_bump_scaling_policy_revision_trg ON apps;
DROP FUNCTION IF EXISTS apps_bump_scaling_policy_revision();
ALTER TABLE IF EXISTS apps DROP CONSTRAINT IF EXISTS apps_scaling_policy_revision_positive;
ALTER TABLE IF EXISTS apps DROP COLUMN IF EXISTS scaling_policy_revision;
-- +goose StatementEnd
