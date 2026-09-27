-- +goose Up
-- +goose StatementBegin

-- CPU quota is enforced by a host cgroup and can be changed for a live VM.
-- Persist a desired revision and a per-node acknowledgement so app PATCHes
-- converge independently of deployment creation and missed pg_notify wakes.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS app_cpu_policy_revision bigint NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'apps_app_cpu_policy_revision_positive'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_app_cpu_policy_revision_positive
            CHECK (app_cpu_policy_revision > 0);
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION apps_bump_cpu_policy_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.cpu_millicores IS DISTINCT FROM OLD.cpu_millicores THEN
        NEW.app_cpu_policy_revision := OLD.app_cpu_policy_revision + 1;
    ELSE
        NEW.app_cpu_policy_revision := OLD.app_cpu_policy_revision;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_bump_cpu_policy_revision_trg ON apps;
CREATE TRIGGER apps_bump_cpu_policy_revision_trg
    BEFORE UPDATE OF cpu_millicores, app_cpu_policy_revision ON apps
    FOR EACH ROW EXECUTE FUNCTION apps_bump_cpu_policy_revision();

CREATE TABLE IF NOT EXISTS app_cpu_policy_node_status (
    app_id             uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    node_id            uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    applied_revision   bigint NOT NULL DEFAULT 0 CHECK (applied_revision >= 0),
    attempted_revision bigint NOT NULL DEFAULT 0 CHECK (attempted_revision >= 0),
    observed_at        timestamptz NOT NULL DEFAULT now(),
    last_error         text NOT NULL DEFAULT '',
    PRIMARY KEY (app_id, node_id),
    CHECK (attempted_revision >= applied_revision)
);

CREATE INDEX IF NOT EXISTS app_cpu_policy_node_status_observed_idx
    ON app_cpu_policy_node_status (observed_at);

CREATE OR REPLACE FUNCTION apps_notify_cpu_policy_changed()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_notify(
        'app_cpu_limit_policy_changed',
        json_build_object('app_id', NEW.id, 'revision', NEW.app_cpu_policy_revision)::text
    );
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_notify_cpu_policy_changed_trg ON apps;
CREATE TRIGGER apps_notify_cpu_policy_changed_trg
    AFTER UPDATE OF cpu_millicores ON apps
    FOR EACH ROW
    WHEN (OLD.cpu_millicores IS DISTINCT FROM NEW.cpu_millicores)
    EXECUTE FUNCTION apps_notify_cpu_policy_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS apps_notify_cpu_policy_changed_trg ON apps;
DROP FUNCTION IF EXISTS apps_notify_cpu_policy_changed();
DROP TABLE IF EXISTS app_cpu_policy_node_status;
DROP TRIGGER IF EXISTS apps_bump_cpu_policy_revision_trg ON apps;
DROP FUNCTION IF EXISTS apps_bump_cpu_policy_revision();
ALTER TABLE IF EXISTS apps DROP CONSTRAINT IF EXISTS apps_app_cpu_policy_revision_positive;
ALTER TABLE IF EXISTS apps DROP COLUMN IF EXISTS app_cpu_policy_revision;
-- +goose StatementEnd
