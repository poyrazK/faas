-- +goose Up
-- +goose StatementBegin

-- The app egress allowlist is already applied to live VMs by schedd, but the
-- current notification-only fan-out can be lost while a scheduler reconnects.
-- Keep the latest desired revision on the app row so independent schedulers
-- can always reconcile from current state rather than relying on event history.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS egress_allowlist_revision bigint NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'apps_egress_allowlist_revision_positive'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_egress_allowlist_revision_positive
            CHECK (egress_allowlist_revision > 0);
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION apps_bump_egress_allowlist_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.egress_allowlist IS DISTINCT FROM OLD.egress_allowlist THEN
        NEW.egress_allowlist_revision := OLD.egress_allowlist_revision + 1;
    ELSE
        -- Callers cannot forge a revision by setting the bookkeeping column.
        NEW.egress_allowlist_revision := OLD.egress_allowlist_revision;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_bump_egress_allowlist_revision_trg ON apps;
CREATE TRIGGER apps_bump_egress_allowlist_revision_trg
    BEFORE UPDATE OF egress_allowlist, egress_allowlist_revision ON apps
    FOR EACH ROW EXECUTE FUNCTION apps_bump_egress_allowlist_revision();

CREATE TABLE IF NOT EXISTS app_egress_policy_node_status (
    app_id                  uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    node_id                 uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    applied_revision        bigint NOT NULL DEFAULT 0 CHECK (applied_revision >= 0),
    attempted_revision      bigint NOT NULL DEFAULT 0 CHECK (attempted_revision >= 0),
    observed_at             timestamptz NOT NULL DEFAULT now(),
    last_error              text NOT NULL DEFAULT '',
    PRIMARY KEY (app_id, node_id),
    CHECK (attempted_revision >= applied_revision)
);

CREATE INDEX IF NOT EXISTS app_egress_policy_node_status_observed_idx
    ON app_egress_policy_node_status (observed_at);

-- Low-latency wake-up for schedd. The app row's revision and allowlist remain
-- authoritative; a periodic reconcile repairs missed notifications.
CREATE OR REPLACE FUNCTION apps_notify_egress_allowlist_changed()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_notify(
        'app_egress_policy_changed',
        json_build_object(
            'app_id', NEW.id,
            'revision', NEW.egress_allowlist_revision
        )::text
    );
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS apps_notify_egress_allowlist_changed_trg ON apps;
CREATE TRIGGER apps_notify_egress_allowlist_changed_trg
    AFTER UPDATE OF egress_allowlist ON apps
    FOR EACH ROW
    WHEN (OLD.egress_allowlist IS DISTINCT FROM NEW.egress_allowlist)
    EXECUTE FUNCTION apps_notify_egress_allowlist_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS apps_notify_egress_allowlist_changed_trg ON apps;
DROP FUNCTION IF EXISTS apps_notify_egress_allowlist_changed();
DROP TABLE IF EXISTS app_egress_policy_node_status;
DROP TRIGGER IF EXISTS apps_bump_egress_allowlist_revision_trg ON apps;
DROP FUNCTION IF EXISTS apps_bump_egress_allowlist_revision();
ALTER TABLE IF EXISTS apps DROP CONSTRAINT IF EXISTS apps_egress_allowlist_revision_positive;
ALTER TABLE IF EXISTS apps DROP COLUMN IF EXISTS egress_allowlist_revision;
-- +goose StatementEnd
