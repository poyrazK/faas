-- ADR-646: admission-owned dependency pins and restart-safe readiness waits.
-- +goose Up
CREATE TABLE deployment_dependency_gates (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    pins jsonb NOT NULL CHECK (jsonb_typeof(pins) = 'array' AND jsonb_array_length(pins) BETWEEN 1 AND 100),
    started_at timestamptz,
    deadline_at timestamptz,
    status text NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'ready', 'failed')),
    blocker text NOT NULL DEFAULT '' CHECK (length(blocker) <= 1024),
    CHECK ((started_at IS NULL AND deadline_at IS NULL) OR
           (started_at IS NOT NULL AND deadline_at IS NOT NULL AND deadline_at > started_at))
);

-- +goose StatementBegin
CREATE FUNCTION check_project_dependency_release() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gate deployment_dependency_gates; pin jsonb; target_status text; target_traffic integer; parked text;
BEGIN
    IF NEW.status <> 'live' OR OLD.status = 'live' OR OLD.serving_ended_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    SELECT * INTO gate FROM deployment_dependency_gates WHERE deployment_id = NEW.id;
    IF NOT FOUND THEN RETURN NEW; END IF;
    IF gate.status = 'failed' OR (gate.status <> 'ready' AND gate.deadline_at <= clock_timestamp()) THEN
        RAISE EXCEPTION 'dependency release gate failed' USING ERRCODE = '23514', CONSTRAINT = 'deployment_dependency_not_ready';
    END IF;
    FOR pin IN SELECT value FROM jsonb_array_elements(gate.pins) ORDER BY value->>'deployment_id' LOOP
        SELECT d.status, d.traffic_percent, coalesce(d.parked_reason, '')
        INTO target_status, target_traffic, parked
        FROM deployments d JOIN apps a ON a.id = d.app_id JOIN apps owner ON owner.id = NEW.app_id
        WHERE d.id = (pin->>'deployment_id')::uuid AND d.app_id = (pin->>'app_id')::uuid
          AND a.account_id = owner.account_id AND a.project_id = owner.project_id AND a.status <> 'deleted'
          AND a.workload_class <> 'job' AND coalesce(a.manifest->>'execution_mode', '') <> 'job'
          AND coalesce(a.preview_pr_number, 0) = coalesce(owner.preview_pr_number, 0)
          AND (coalesce(a.preview_of_slug, '') = '') = (coalesce(owner.preview_of_slug, '') = '')
          AND d.scope = NEW.scope AND d.environment_workload_runtime IS NULL
        FOR SHARE OF d;
        IF NOT FOUND OR target_status <> 'live' OR target_traffic <= 0 OR parked <> '' THEN
            RAISE EXCEPTION 'dependency deployment is not ready' USING ERRCODE = '23514', CONSTRAINT = 'deployment_dependency_not_ready';
        END IF;
    END LOOP;
    RETURN NEW;
END;
$$;
CREATE TRIGGER deployment_dependency_release_check BEFORE UPDATE OF status ON deployments
FOR EACH ROW EXECUTE FUNCTION check_project_dependency_release();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER deployment_dependency_release_check ON deployments;
DROP FUNCTION check_project_dependency_release();
DROP TABLE deployment_dependency_gates;
