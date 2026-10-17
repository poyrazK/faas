-- +goose Up
ALTER TABLE deployments ADD COLUMN snapshot_recovery_generation bigint NOT NULL DEFAULT 0
    CONSTRAINT deployments_snapshot_recovery_generation_check CHECK (snapshot_recovery_generation >= 0);
ALTER TABLE snapshots ADD COLUMN recovery_generation bigint NOT NULL DEFAULT 0
    CONSTRAINT snapshots_recovery_generation_check CHECK (recovery_generation >= 0);
ALTER TABLE operator_intents ADD COLUMN cold_boot_generation bigint
    CONSTRAINT operator_intents_cold_boot_generation_check CHECK (cold_boot_generation > 0);

-- This is boot admission lineage, not a timestamp inferred from readiness.
-- Keep snapshot_id as evidence even after ordinary snapshot GC removes its row.
CREATE TABLE instance_snapshot_recovery (
    instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    wake_id uuid NOT NULL CHECK (wake_id <> '00000000-0000-0000-0000-000000000000'),
    generation bigint NOT NULL CHECK (generation >= 0),
    snapshot_id uuid CHECK (snapshot_id IS NULL OR snapshot_id <> '00000000-0000-0000-0000-000000000000')
);

-- Fence legacy and direct cache writers as well as the normal imaged publisher.
-- The deployment lock serializes insertion with generation change/invalidation.
-- +goose StatementBegin
CREATE FUNCTION snapshot_recovery_generation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_generation bigint;
BEGIN
    IF NOT NEW.stale THEN
        SELECT snapshot_recovery_generation INTO current_generation
          FROM deployments WHERE id = NEW.deployment_id FOR SHARE NOWAIT;
        IF NEW.recovery_generation IS DISTINCT FROM current_generation THEN
            RAISE EXCEPTION 'snapshot recovery generation changed'
                USING ERRCODE = '23514', CONSTRAINT = 'snapshots_recovery_generation_current';
        END IF;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER snapshot_recovery_generation_guard
    BEFORE INSERT OR UPDATE OF stale, recovery_generation, deployment_id ON snapshots
    FOR EACH ROW EXECUTE FUNCTION snapshot_recovery_generation_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM deployments WHERE snapshot_recovery_generation > 0)
       OR EXISTS (SELECT 1 FROM operator_intents WHERE cold_boot_generation IS NOT NULL) THEN
        RAISE EXCEPTION 'Preserve active cold-boot recovery boundaries before downgrade';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER snapshot_recovery_generation_guard ON snapshots;
DROP FUNCTION snapshot_recovery_generation_guard();
DROP TABLE instance_snapshot_recovery;
ALTER TABLE operator_intents DROP COLUMN cold_boot_generation;
ALTER TABLE snapshots DROP COLUMN recovery_generation;
ALTER TABLE deployments DROP COLUMN snapshot_recovery_generation;
