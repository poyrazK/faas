-- +goose Up
-- ADR-531: legacy app-only work readers and mutations own production.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_invocation_headers_own_stage(owner_app uuid, pin_headers jsonb)
RETURNS boolean LANGUAGE sql STABLE AS $$
SELECT
    EXISTS(SELECT 1 FROM deployments stage
        WHERE stage.app_id=owner_app AND stage.scope NOT IN ('production','default')
            AND EXISTS(SELECT 1 FROM jsonb_each_text(CASE WHEN jsonb_typeof(pin_headers)='object' THEN pin_headers ELSE '{}'::jsonb END) pin
                WHERE lower(pin.key)='x-gregale-revision' AND translate(regexp_replace(regexp_replace(lower(pin.value), '[[:space:]{}]', '', 'g'), '^urn:uuid:', ''), '-', '')=replace(stage.id::text,'-','')))
    OR EXISTS(SELECT 1 FROM project_release_sets stage JOIN apps owner
        ON owner.project_id=stage.project_id AND owner.account_id=stage.account_id
        WHERE owner.id=owner_app AND stage.environment_slug NOT IN ('production','default')
            AND EXISTS(SELECT 1 FROM jsonb_each_text(CASE WHEN jsonb_typeof(pin_headers)='object' THEN pin_headers ELSE '{}'::jsonb END) pin
                WHERE lower(pin.key)='x-gregale-release' AND translate(regexp_replace(regexp_replace(lower(pin.value), '[[:space:]{}]', '', 'g'), '^urn:uuid:', ''), '-', '')=replace(stage.id::text,'-','')));
$$;
-- +goose StatementEnd

CREATE OR REPLACE VIEW production_invocation_work AS
SELECT i.* FROM invocations i
WHERE i.environment_id IS NULL
    AND NOT EXISTS(SELECT 1 FROM invocation_environment_queue_admissions p WHERE p.invocation_id=i.id)
    AND NOT EXISTS(SELECT 1 FROM invocation_work_environment_admissions p WHERE p.invocation_id=i.id)
    AND NOT faas_invocation_headers_own_stage(i.app_id,i.headers);

CREATE OR REPLACE VIEW invocations_pending_per_app AS
SELECT app_id,source,count(*) AS pending FROM production_invocation_work
WHERE state IN ('pending','dispatching') GROUP BY app_id,source;

ALTER TABLE dead_letter_events ADD COLUMN IF NOT EXISTS environment_owned boolean NOT NULL DEFAULT false;
-- Ownership survives source retention and cannot be cleared by a new failure.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_retain_dead_letter_environment_ownership() RETURNS trigger AS $$
BEGIN
    IF TG_OP='UPDATE' THEN NEW.environment_owned := NEW.environment_owned OR OLD.environment_owned; END IF;
    NEW.environment_owned := NEW.environment_owned OR (NEW.source='invocation' AND (faas_invocation_headers_own_stage(NEW.app_id,NEW.headers)
        OR EXISTS(SELECT 1 FROM invocations i WHERE i.id=NEW.source_id
            AND NOT EXISTS(SELECT 1 FROM production_invocation_work p WHERE p.id=i.id))));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE OR REPLACE TRIGGER dead_letter_events_environment_ownership BEFORE INSERT OR UPDATE ON dead_letter_events
    FOR EACH ROW EXECUTE FUNCTION faas_retain_dead_letter_environment_ownership();
-- +goose StatementEnd
UPDATE dead_letter_events e SET environment_owned=true WHERE e.source='invocation'
    AND (faas_invocation_headers_own_stage(e.app_id,e.headers)
        OR EXISTS(SELECT 1 FROM invocations i WHERE i.id=e.source_id
            AND NOT EXISTS(SELECT 1 FROM production_invocation_work p WHERE p.id=i.id)));
CREATE OR REPLACE VIEW production_dead_letter_events AS SELECT e.* FROM dead_letter_events e
WHERE NOT e.environment_owned AND (e.source<>'invocation' OR (NOT faas_invocation_headers_own_stage(e.app_id,e.headers)
    AND NOT EXISTS(SELECT 1 FROM invocations i WHERE i.id=e.source_id
        AND NOT EXISTS(SELECT 1 FROM production_invocation_work p WHERE p.id=i.id))));

-- +goose Down
DROP VIEW production_dead_letter_events;
DROP TRIGGER dead_letter_events_environment_ownership ON dead_letter_events;
DROP FUNCTION faas_retain_dead_letter_environment_ownership();
ALTER TABLE dead_letter_events DROP COLUMN environment_owned;
CREATE OR REPLACE VIEW invocations_pending_per_app AS
SELECT app_id,source,count(*) AS pending FROM invocations
WHERE state IN ('pending','dispatching') GROUP BY app_id,source;
DROP VIEW production_invocation_work;
DROP FUNCTION faas_invocation_headers_own_stage(uuid,jsonb);
