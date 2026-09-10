-- +goose Up
-- +goose StatementBegin

ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS public_id UUID;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS kind TEXT;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS title TEXT;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS impact TEXT;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS affected_components TEXT[];
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS lifecycle_state TEXT;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS starts_at TIMESTAMPTZ;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS scheduled_start_at TIMESTAMPTZ;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS scheduled_end_at TIMESTAMPTZ;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS create_idempotency_key TEXT;
ALTER TABLE status_incidents ADD COLUMN IF NOT EXISTS created_by TEXT;

-- Rolling deploy compatibility: the pre-v2 store inserts only
-- (component,severity,message). Populate the additive public columns before
-- NOT NULL/CHECK validation so an older apid or gregalectl can safely coexist
-- with the new schema while the backend-first rollout advances.
CREATE OR REPLACE FUNCTION populate_legacy_status_incident_public_fields() RETURNS trigger AS $$
BEGIN
    NEW.public_id := COALESCE(NEW.public_id, gen_random_uuid());
    NEW.kind := COALESCE(NEW.kind, 'incident');
    NEW.title := COALESCE(NEW.title, left(COALESCE(NULLIF(btrim(NEW.message), ''), 'Service incident'), 160));
    NEW.impact := COALESCE(NEW.impact, CASE NEW.severity
        WHEN 'full_outage' THEN 'major_outage'
        WHEN 'partial_outage' THEN 'partial_outage'
        WHEN 'maintenance' THEN 'maintenance'
        ELSE 'degraded' END);
    NEW.affected_components := COALESCE(NEW.affected_components, CASE WHEN NEW.component = 'faas-control-plane' THEN ARRAY[
        'api_console','deployments','app_execution','networking','observability'
    ]::TEXT[] ELSE ARRAY[CASE NEW.component
        WHEN 'apid' THEN 'api_console'
        WHEN 'builderd' THEN 'deployments'
        WHEN 'imaged' THEN 'deployments'
        WHEN 'schedd' THEN 'app_execution'
        WHEN 'vmmd' THEN 'app_execution'
        WHEN 'gatewayd' THEN 'networking'
        WHEN 'meterd' THEN 'observability'
        ELSE 'api_console' END]::TEXT[] END);
    NEW.lifecycle_state := COALESCE(NEW.lifecycle_state, CASE WHEN NEW.resolved_at IS NULL THEN 'investigating' ELSE 'resolved' END);
    IF NEW.kind = 'incident' THEN
        NEW.starts_at := COALESCE(NEW.starts_at, NEW.posted_at, now());
    END IF;
    NEW.updated_at := COALESCE(NEW.updated_at, NEW.resolved_at, NEW.posted_at, now());
    NEW.create_idempotency_key := COALESCE(NEW.create_idempotency_key, 'legacy:' || gen_random_uuid()::text);
    NEW.created_by := COALESCE(NEW.created_by, 'legacy-api');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS status_incidents_legacy_public_defaults ON status_incidents;
CREATE TRIGGER status_incidents_legacy_public_defaults
BEFORE INSERT ON status_incidents
FOR EACH ROW EXECUTE FUNCTION populate_legacy_status_incident_public_fields();

UPDATE status_incidents
SET public_id = COALESCE(public_id, gen_random_uuid()),
    kind = COALESCE(kind, 'incident'),
    title = COALESCE(title, left(COALESCE(NULLIF(btrim(message), ''), 'Service incident'), 160)),
    impact = COALESCE(impact, CASE severity
        WHEN 'full_outage' THEN 'major_outage'
        WHEN 'partial_outage' THEN 'partial_outage'
        WHEN 'maintenance' THEN 'maintenance'
        ELSE 'degraded' END),
    affected_components = COALESCE(affected_components, CASE WHEN component = 'faas-control-plane' THEN ARRAY[
        'api_console','deployments','app_execution','networking','observability'
    ]::TEXT[] ELSE ARRAY[CASE component
        WHEN 'apid' THEN 'api_console'
        WHEN 'builderd' THEN 'deployments'
        WHEN 'imaged' THEN 'deployments'
        WHEN 'schedd' THEN 'app_execution'
        WHEN 'vmmd' THEN 'app_execution'
        WHEN 'gatewayd' THEN 'networking'
        WHEN 'meterd' THEN 'observability'
        ELSE 'api_console' END]::TEXT[] END),
    lifecycle_state = COALESCE(lifecycle_state, CASE WHEN resolved_at IS NULL THEN 'investigating' ELSE 'resolved' END),
    starts_at = COALESCE(starts_at, posted_at),
    updated_at = COALESCE(updated_at, resolved_at, posted_at),
    create_idempotency_key = COALESCE(create_idempotency_key, 'legacy:' || id::text),
    created_by = COALESCE(created_by, 'legacy-import');

ALTER TABLE status_incidents ALTER COLUMN public_id SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN kind SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN title SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN impact SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN affected_components SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN lifecycle_state SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN create_idempotency_key SET NOT NULL;
ALTER TABLE status_incidents ALTER COLUMN created_by SET NOT NULL;

ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_public_id_key;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_public_id_key UNIQUE (public_id);
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_create_idempotency_key_key;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_create_idempotency_key_key UNIQUE (create_idempotency_key);
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_kind_chk;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_kind_chk CHECK (kind IN ('incident', 'maintenance'));
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_title_len_chk;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_title_len_chk CHECK (char_length(btrim(title)) BETWEEN 1 AND 160);
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_impact_chk;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_impact_chk CHECK (impact IN ('maintenance','degraded','partial_outage','major_outage'));
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_affected_components_chk;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_affected_components_chk CHECK (
    cardinality(affected_components) > 0 AND affected_components <@ ARRAY[
        'api_console','deployments','app_execution','networking','observability'
    ]::TEXT[]
);
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_lifecycle_chk;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_lifecycle_chk CHECK (
    (kind = 'incident' AND lifecycle_state IN ('investigating','identified','monitoring','resolved')) OR
    (kind = 'maintenance' AND lifecycle_state IN ('scheduled','in_progress','completed','cancelled'))
);
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_schedule_chk;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_schedule_chk CHECK (
    (kind = 'incident' AND starts_at IS NOT NULL AND scheduled_start_at IS NULL AND scheduled_end_at IS NULL) OR
    (kind = 'maintenance' AND starts_at IS NULL AND scheduled_start_at IS NOT NULL AND scheduled_end_at > scheduled_start_at)
);
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_terminal_time_chk;
ALTER TABLE status_incidents ADD CONSTRAINT status_incidents_terminal_time_chk CHECK (
    (lifecycle_state IN ('resolved','completed','cancelled')) = (resolved_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS status_incidents_public_active_idx
    ON status_incidents(updated_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS status_incidents_public_history_idx
    ON status_incidents(resolved_at DESC) WHERE resolved_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS status_incident_updates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id BIGINT NOT NULL REFERENCES status_incidents(id) ON DELETE RESTRICT,
    lifecycle_state TEXT NOT NULL,
    message TEXT NOT NULL,
    posted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    CONSTRAINT status_incident_updates_state_chk CHECK (lifecycle_state IN (
        'investigating','identified','monitoring','resolved',
        'scheduled','in_progress','completed','cancelled'
    )),
    CONSTRAINT status_incident_updates_message_len_chk CHECK (char_length(btrim(message)) BETWEEN 1 AND 1024)
);

INSERT INTO status_incident_updates (incident_id, lifecycle_state, message, posted_at, actor, idempotency_key)
SELECT id,
       CASE WHEN lifecycle_state = 'resolved' THEN 'investigating' ELSE lifecycle_state END,
       COALESCE(NULLIF(btrim(message), ''), title), posted_at, created_by, create_idempotency_key
FROM status_incidents
ON CONFLICT (idempotency_key) DO NOTHING;

INSERT INTO status_incident_updates (incident_id, lifecycle_state, message, posted_at, actor, idempotency_key)
SELECT id, 'resolved', 'Resolved (legacy event)', resolved_at, created_by, create_idempotency_key || ':resolved'
FROM status_incidents
WHERE lifecycle_state = 'resolved' AND resolved_at IS NOT NULL
ON CONFLICT (idempotency_key) DO NOTHING;

CREATE OR REPLACE FUNCTION append_initial_status_incident_update() RETURNS trigger AS $$
BEGIN
    INSERT INTO status_incident_updates (
        incident_id, lifecycle_state, message, posted_at, actor, idempotency_key
    ) VALUES (
        NEW.id,
        NEW.lifecycle_state,
        COALESCE(NULLIF(btrim(NEW.message), ''), NEW.title),
        NEW.posted_at,
        NEW.created_by,
        NEW.create_idempotency_key
    ) ON CONFLICT (idempotency_key) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS status_incidents_initial_update ON status_incidents;
CREATE TRIGGER status_incidents_initial_update
AFTER INSERT ON status_incidents
FOR EACH ROW EXECUTE FUNCTION append_initial_status_incident_update();

-- An old binary resolves by setting resolved_at only. Keep that write legal
-- under the new lifecycle constraint and append the public terminal update.
CREATE OR REPLACE FUNCTION populate_legacy_status_incident_resolution() RETURNS trigger AS $$
BEGIN
    IF OLD.resolved_at IS NULL AND NEW.resolved_at IS NOT NULL
       AND NEW.lifecycle_state = OLD.lifecycle_state THEN
        NEW.lifecycle_state := CASE WHEN NEW.kind = 'maintenance' THEN 'completed' ELSE 'resolved' END;
        NEW.updated_at := NEW.resolved_at;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS status_incidents_legacy_resolution_fields ON status_incidents;
CREATE TRIGGER status_incidents_legacy_resolution_fields
BEFORE UPDATE OF resolved_at ON status_incidents
FOR EACH ROW EXECUTE FUNCTION populate_legacy_status_incident_resolution();

CREATE OR REPLACE FUNCTION append_legacy_status_incident_resolution() RETURNS trigger AS $$
BEGIN
    IF OLD.resolved_at IS NULL AND NEW.resolved_at IS NOT NULL THEN
        INSERT INTO status_incident_updates (
            incident_id, lifecycle_state, message, posted_at, actor, idempotency_key
        ) SELECT NEW.id, NEW.lifecycle_state, 'Resolved', NEW.resolved_at, 'legacy-api',
                 'legacy-resolve:' || NEW.id::text
          WHERE NOT EXISTS (
              SELECT 1 FROM status_incident_updates
              WHERE incident_id = NEW.id
                AND lifecycle_state = NEW.lifecycle_state
                AND posted_at = NEW.resolved_at
          )
        ON CONFLICT (idempotency_key) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS status_incidents_legacy_resolution_update ON status_incidents;
CREATE TRIGGER status_incidents_legacy_resolution_update
AFTER UPDATE OF resolved_at ON status_incidents
FOR EACH ROW EXECUTE FUNCTION append_legacy_status_incident_resolution();

CREATE INDEX IF NOT EXISTS status_incident_updates_timeline_idx
    ON status_incident_updates(incident_id, posted_at, id);

CREATE TABLE IF NOT EXISTS status_observation_buckets (
    component TEXT NOT NULL,
    bucket_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL,
    has_telemetry BOOLEAN NOT NULL,
    PRIMARY KEY (component, bucket_at),
    CONSTRAINT status_observation_buckets_component_chk CHECK (component IN (
        'api_console','deployments','app_execution','networking','observability'
    )),
    CONSTRAINT status_observation_buckets_status_chk CHECK (status IN (
        'operational','maintenance','degraded','partial_outage','major_outage','unknown'
    )),
    CONSTRAINT status_observation_buckets_five_minute_chk CHECK (
        mod(extract(epoch FROM bucket_at)::bigint, 300) = 0
    )
);
CREATE INDEX IF NOT EXISTS status_observation_buckets_day_idx
    ON status_observation_buckets((bucket_at AT TIME ZONE 'UTC')::date, component);

CREATE OR REPLACE FUNCTION reject_status_update_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'status incident updates are append-only' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS status_incident_updates_append_only ON status_incident_updates;
CREATE TRIGGER status_incident_updates_append_only
BEFORE UPDATE OR DELETE ON status_incident_updates
FOR EACH ROW EXECUTE FUNCTION reject_status_update_mutation();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS status_incidents_initial_update ON status_incidents;
DROP FUNCTION IF EXISTS append_initial_status_incident_update();
DROP TRIGGER IF EXISTS status_incidents_legacy_resolution_update ON status_incidents;
DROP FUNCTION IF EXISTS append_legacy_status_incident_resolution();
DROP TRIGGER IF EXISTS status_incidents_legacy_resolution_fields ON status_incidents;
DROP FUNCTION IF EXISTS populate_legacy_status_incident_resolution();
DROP TRIGGER IF EXISTS status_incident_updates_append_only ON status_incident_updates;
DROP FUNCTION IF EXISTS reject_status_update_mutation();
DROP TABLE IF EXISTS status_observation_buckets;
DROP TABLE IF EXISTS status_incident_updates;
DROP TRIGGER IF EXISTS status_incidents_legacy_public_defaults ON status_incidents;
DROP FUNCTION IF EXISTS populate_legacy_status_incident_public_fields();
DROP INDEX IF EXISTS status_incidents_public_history_idx;
DROP INDEX IF EXISTS status_incidents_public_active_idx;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_schedule_chk;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_terminal_time_chk;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_lifecycle_chk;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_affected_components_chk;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_impact_chk;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_title_len_chk;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_kind_chk;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_create_idempotency_key_key;
ALTER TABLE status_incidents DROP CONSTRAINT IF EXISTS status_incidents_public_id_key;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS created_by;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS create_idempotency_key;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS updated_at;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS scheduled_end_at;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS scheduled_start_at;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS starts_at;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS lifecycle_state;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS affected_components;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS impact;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS title;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS kind;
ALTER TABLE status_incidents DROP COLUMN IF EXISTS public_id;
-- +goose StatementEnd
