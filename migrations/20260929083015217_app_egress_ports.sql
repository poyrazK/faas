-- +goose Up
-- +goose StatementBegin

-- ADR-361: extra TCP destination ports an app declares on top of the base web
-- ports every guest may reach. apid stores the canonical form (sorted, no base
-- ports, no forbidden ports); vmmd filters forbidden ports again when it
-- renders the per-instance egress_ports set.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS egress_ports integer[] NOT NULL DEFAULT '{}';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'apps_egress_ports_valid'
          AND conrelid = 'apps'::regclass
    ) THEN
        ALTER TABLE apps
            ADD CONSTRAINT apps_egress_ports_valid
            CHECK (
                cardinality(egress_ports) <= 64
                AND 1 <= ALL (egress_ports)
                AND 65535 >= ALL (egress_ports)
            );
    END IF;
END
$$;

-- The app egress policy revision (migration 20260926093000001) now covers the
-- extra ports as well, so the schedd reconciler pushes port changes to live
-- instances through the same convergence path as the allowlist.
CREATE OR REPLACE FUNCTION apps_bump_egress_allowlist_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.egress_allowlist IS DISTINCT FROM OLD.egress_allowlist
       OR NEW.egress_ports IS DISTINCT FROM OLD.egress_ports THEN
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
    BEFORE UPDATE OF egress_allowlist, egress_ports, egress_allowlist_revision ON apps
    FOR EACH ROW EXECUTE FUNCTION apps_bump_egress_allowlist_revision();

DROP TRIGGER IF EXISTS apps_notify_egress_allowlist_changed_trg ON apps;
CREATE TRIGGER apps_notify_egress_allowlist_changed_trg
    AFTER UPDATE OF egress_allowlist, egress_ports ON apps
    FOR EACH ROW
    WHEN (OLD.egress_allowlist IS DISTINCT FROM NEW.egress_allowlist
          OR OLD.egress_ports IS DISTINCT FROM NEW.egress_ports)
    EXECUTE FUNCTION apps_notify_egress_allowlist_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS apps_notify_egress_allowlist_changed_trg ON apps;
CREATE TRIGGER apps_notify_egress_allowlist_changed_trg
    AFTER UPDATE OF egress_allowlist ON apps
    FOR EACH ROW
    WHEN (OLD.egress_allowlist IS DISTINCT FROM NEW.egress_allowlist)
    EXECUTE FUNCTION apps_notify_egress_allowlist_changed();

DROP TRIGGER IF EXISTS apps_bump_egress_allowlist_revision_trg ON apps;
CREATE OR REPLACE FUNCTION apps_bump_egress_allowlist_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.egress_allowlist IS DISTINCT FROM OLD.egress_allowlist THEN
        NEW.egress_allowlist_revision := OLD.egress_allowlist_revision + 1;
    ELSE
        NEW.egress_allowlist_revision := OLD.egress_allowlist_revision;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER apps_bump_egress_allowlist_revision_trg
    BEFORE UPDATE OF egress_allowlist, egress_allowlist_revision ON apps
    FOR EACH ROW EXECUTE FUNCTION apps_bump_egress_allowlist_revision();

ALTER TABLE IF EXISTS apps DROP CONSTRAINT IF EXISTS apps_egress_ports_valid;
ALTER TABLE IF EXISTS apps DROP COLUMN IF EXISTS egress_ports;
-- +goose StatementEnd
