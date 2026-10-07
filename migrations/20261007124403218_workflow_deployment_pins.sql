-- +goose Up
-- ADR-648: workflow-private code retention never extends public revision TTLs.
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS deployment_id uuid;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='workflow_runs'::regclass AND conname='workflow_runs_deployment_id_check') THEN
        ALTER TABLE workflow_runs ADD CONSTRAINT workflow_runs_deployment_id_check
            CHECK (deployment_id IS NULL OR deployment_id <> '00000000-0000-0000-0000-000000000000');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='workflow_runs'::regclass AND conname='workflow_runs_deployment_owner_fk') THEN
        ALTER TABLE workflow_runs ADD CONSTRAINT workflow_runs_deployment_owner_fk
            FOREIGN KEY(deployment_id,app_id) REFERENCES deployments(id,app_id) DEFERRABLE INITIALLY DEFERRED;
    END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS workflow_runs_deployment_retention_idx ON workflow_runs(deployment_id) WHERE deployment_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS workflow_code_pins (
    deployment_id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL CHECK(isfinite(expires_at)),
    FOREIGN KEY(deployment_id,app_id) REFERENCES deployments(id,app_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS workflow_code_pins_app_expiry_idx ON workflow_code_pins(app_id,expires_at);
CREATE TABLE IF NOT EXISTS workflow_event_code_refs (
    outbox_id bigint NOT NULL REFERENCES event_fanout_outbox(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    deployment_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    PRIMARY KEY(outbox_id,deployment_id),
    FOREIGN KEY(deployment_id,app_id) REFERENCES deployments(id,app_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS workflow_event_code_refs_deployment_idx ON workflow_event_code_refs(deployment_id);

-- Normalize captured identities once; retirement must not expand all retained
-- event JSON for every deployment. Missing/foreign identities cannot retain code.
INSERT INTO workflow_event_code_refs(outbox_id,deployment_id,app_id)
SELECT DISTINCT o.id,d.id,a.id FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(o.recipient_snapshot) r
JOIN apps a ON a.id::text=r->>'app_id' AND a.account_id=o.account_id AND a.account_id::text=r->>'account_id' AND a.status<>'deleted'
JOIN deployments d ON d.id::text=r->>'deployment_id' AND d.app_id=a.id
WHERE r ? 'workflow'
ON CONFLICT DO NOTHING;


-- +goose StatementBegin
CREATE OR REPLACE VIEW workflow_retained_deployment_refs AS
SELECT DISTINCT w.deployment_id FROM workflow_runs w
JOIN apps a ON a.id=w.app_id AND a.status<>'deleted'
JOIN deployments d ON d.id=w.deployment_id AND d.app_id=w.app_id
UNION
SELECT DISTINCT r.deployment_id FROM workflow_event_code_refs r
JOIN apps a ON a.id=r.app_id AND a.status<>'deleted';

CREATE OR REPLACE VIEW durable_work_retained_deployment_refs AS
SELECT deployment_id FROM customer_operation_retained_deployment_refs
UNION SELECT deployment_id FROM workflow_retained_deployment_refs;

CREATE OR REPLACE VIEW deployment_code_pin_deadlines AS
SELECT deployment_id,app_id,max(expires_at) AS expires_at FROM (
    SELECT deployment_id,app_id,expires_at FROM deployment_revision_pins
    UNION ALL SELECT deployment_id,app_id,expires_at FROM customer_operation_code_pins
    UNION ALL SELECT deployment_id,app_id,expires_at FROM workflow_code_pins
) receipts GROUP BY deployment_id,app_id;

CREATE OR REPLACE FUNCTION pin_workflow_code(app uuid, deployment uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF deployment IS NULL THEN RETURN; END IF;
    -- Admission and retirement serialize app first, deployment second, then
    -- immutable artifact fences. References commit atomically with the pin.
    PERFORM 1 FROM apps WHERE id=app AND status<>'deleted' FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'workflow deployment owner is unavailable' USING ERRCODE='23514', CONSTRAINT='workflow_code_available';
    END IF;
    PERFORM 1 FROM deployments WHERE id=deployment AND app_id=app AND status='live' AND deleted_at IS NULL FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'workflow deployment is unavailable' USING ERRCODE='23514', CONSTRAINT='workflow_code_available';
    END IF;
    PERFORM require_deployment_layer_artifacts(deployment);
    INSERT INTO workflow_code_pins(deployment_id,app_id,expires_at) VALUES(deployment,app,now())
    ON CONFLICT(deployment_id) DO NOTHING;
END;
$$;

CREATE OR REPLACE FUNCTION guard_workflow_code_pin() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' THEN
        IF NEW.deployment_id IS DISTINCT FROM OLD.deployment_id OR NEW.app_id IS DISTINCT FROM OLD.app_id THEN
            RAISE EXCEPTION 'workflow deployment is immutable' USING ERRCODE='23514', CONSTRAINT='workflow_code_immutable';
        END IF;
    ELSE
        PERFORM pin_workflow_code(NEW.app_id,NEW.deployment_id);
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS workflow_runs_code_guard ON workflow_runs;
CREATE TRIGGER workflow_runs_code_guard BEFORE INSERT OR UPDATE OF deployment_id,app_id ON workflow_runs
FOR EACH ROW EXECUTE FUNCTION guard_workflow_code_pin();

CREATE OR REPLACE FUNCTION guard_workflow_event_code_pins() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipient jsonb;
BEGIN
    DELETE FROM workflow_event_code_refs WHERE outbox_id=NEW.id;
    FOR recipient IN SELECT r FROM jsonb_array_elements(NEW.recipient_snapshot) r
        WHERE r ? 'workflow' AND coalesce(r->>'deployment_id','')<>'' ORDER BY r->>'app_id',r->>'deployment_id'
    LOOP
        IF NOT EXISTS(SELECT 1 FROM apps a WHERE a.id::text=recipient->>'app_id'
            AND a.account_id=NEW.account_id AND a.account_id::text=recipient->>'account_id') THEN
            RAISE EXCEPTION 'workflow event owner is unavailable' USING ERRCODE='23514', CONSTRAINT='workflow_code_available';
        END IF;
        PERFORM pin_workflow_code((recipient->>'app_id')::uuid,(recipient->>'deployment_id')::uuid);
        INSERT INTO workflow_event_code_refs(outbox_id,deployment_id,app_id)
        VALUES(NEW.id,(recipient->>'deployment_id')::uuid,(recipient->>'app_id')::uuid) ON CONFLICT DO NOTHING;
    END LOOP;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS event_fanout_workflow_code_guard ON event_fanout_outbox;
CREATE TRIGGER event_fanout_workflow_code_guard AFTER INSERT OR UPDATE OF recipient_snapshot ON event_fanout_outbox
FOR EACH ROW EXECUTE FUNCTION guard_workflow_event_code_pins();

-- Existing event snapshots already carry captured identity. Keep only code
-- still available at migration time; never guess a pin for historical runs or
-- resurrect artifacts already deleted. Unavailable captured code fails closed.
INSERT INTO workflow_code_pins(deployment_id,app_id,expires_at)
SELECT d.id,d.app_id,now() FROM deployments d JOIN workflow_retained_deployment_refs r ON r.deployment_id=d.id
WHERE d.status='live' AND d.deleted_at IS NULL ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER event_fanout_workflow_code_guard ON event_fanout_outbox;
DROP FUNCTION guard_workflow_event_code_pins();
DROP TRIGGER workflow_runs_code_guard ON workflow_runs;
DROP FUNCTION guard_workflow_code_pin();
DROP FUNCTION pin_workflow_code(uuid,uuid);
CREATE OR REPLACE VIEW deployment_code_pin_deadlines AS
SELECT deployment_id,app_id,max(expires_at) AS expires_at FROM (
    SELECT deployment_id,app_id,expires_at FROM deployment_revision_pins
    UNION ALL SELECT deployment_id,app_id,expires_at FROM customer_operation_code_pins
) receipts GROUP BY deployment_id,app_id;
DROP VIEW durable_work_retained_deployment_refs;
DROP VIEW workflow_retained_deployment_refs;
DROP TABLE workflow_event_code_refs;
DROP TABLE workflow_code_pins;
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_runs_deployment_owner_fk;
ALTER TABLE workflow_runs DROP COLUMN deployment_id;
-- +goose StatementEnd
