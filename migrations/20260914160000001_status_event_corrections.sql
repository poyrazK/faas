-- +goose Up
-- +goose StatementBegin
ALTER TABLE status_incident_updates
    ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS impact TEXT,
    ADD COLUMN IF NOT EXISTS affected_components TEXT[];

ALTER TABLE status_incident_updates
    DROP CONSTRAINT IF EXISTS status_incident_updates_impact_chk;
ALTER TABLE status_incident_updates
    ADD CONSTRAINT status_incident_updates_impact_chk CHECK (
        impact IS NULL OR impact IN ('maintenance','degraded','partial_outage','major_outage')
    );
ALTER TABLE status_incident_updates
    DROP CONSTRAINT IF EXISTS status_incident_updates_components_chk;
ALTER TABLE status_incident_updates
    ADD CONSTRAINT status_incident_updates_components_chk CHECK (
        affected_components IS NULL OR cardinality(affected_components) > 0
    );
ALTER TABLE status_incident_updates
    DROP CONSTRAINT IF EXISTS status_incident_updates_edited_at_chk;
ALTER TABLE status_incident_updates
    ADD CONSTRAINT status_incident_updates_edited_at_chk CHECK (
        edited_at IS NULL OR edited_at >= posted_at
    );

ALTER TABLE status_incidents
    ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ;
ALTER TABLE status_incidents
    DROP CONSTRAINT IF EXISTS status_incidents_edited_at_chk;
ALTER TABLE status_incidents
    ADD CONSTRAINT status_incidents_edited_at_chk CHECK (
        edited_at IS NULL OR edited_at >= posted_at
    );

-- Timeline rows remain undeletable. A correction may change only message and
-- edited_at; lifecycle, attribution, ordering, actor, and idempotency identity
-- stay immutable.
CREATE OR REPLACE FUNCTION reject_status_update_mutation() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'status incident updates cannot be deleted' USING ERRCODE = '55000';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.incident_id IS DISTINCT FROM OLD.incident_id
       OR NEW.lifecycle_state IS DISTINCT FROM OLD.lifecycle_state
       OR NEW.posted_at IS DISTINCT FROM OLD.posted_at
       OR NEW.actor IS DISTINCT FROM OLD.actor
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.impact IS DISTINCT FROM OLD.impact
       OR NEW.affected_components IS DISTINCT FROM OLD.affected_components
       OR NEW.edited_at IS NULL THEN
        RAISE EXCEPTION 'status incident update correction may change only message and edited_at' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_status_update_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'status incident updates are append-only' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;

ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_edited_at_chk;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS edited_at;
ALTER TABLE status_incident_updates DROP CONSTRAINT IF EXISTS status_incident_updates_edited_at_chk;
ALTER TABLE status_incident_updates DROP CONSTRAINT IF EXISTS status_incident_updates_components_chk;
ALTER TABLE status_incident_updates DROP CONSTRAINT IF EXISTS status_incident_updates_impact_chk;
ALTER TABLE status_incident_updates DROP COLUMN IF EXISTS affected_components;
ALTER TABLE status_incident_updates DROP COLUMN IF EXISTS impact;
ALTER TABLE status_incident_updates DROP COLUMN IF EXISTS edited_at;
-- +goose StatementEnd
