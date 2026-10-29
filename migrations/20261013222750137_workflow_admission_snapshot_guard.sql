-- migration-compat: contract
-- ADR-727 / ADR-570: workflow code admission against repeatable-read cutover.
-- Cutover serializes with admission through the app's workflow admission lock
-- and then reads retained workflow references. ADR-570 cutovers read inside a
-- repeatable-read snapshot taken before that lock, so each admission bumps one
-- per-app guard row and such a cutover locks it: an admission committed after
-- its snapshot raises a serialization failure and the cutover retries.
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS workflow_code_admission_guard (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0)
);
-- +goose StatementBegin
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
    -- A repeatable-read cutover that started earlier detects this admission.
    INSERT INTO workflow_code_admission_guard(app_id) VALUES(app)
    ON CONFLICT(app_id) DO UPDATE SET generation=workflow_code_admission_guard.generation+1;
END;
$$;

CREATE OR REPLACE FUNCTION guard_workflow_admission_snapshot(app uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('transaction_isolation')='read committed' THEN
        RETURN;
    END IF;
    INSERT INTO workflow_code_admission_guard(app_id) VALUES(app) ON CONFLICT(app_id) DO NOTHING;
    PERFORM 1 FROM workflow_code_admission_guard WHERE app_id=app FOR SHARE;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
-- +goose StatementEnd
DROP FUNCTION IF EXISTS guard_workflow_admission_snapshot(uuid);
DROP TABLE IF EXISTS workflow_code_admission_guard;
