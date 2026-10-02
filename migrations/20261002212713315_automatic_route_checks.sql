-- +goose Up
CREATE TABLE IF NOT EXISTS automatic_route_checks (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    request_id uuid NOT NULL DEFAULT gen_random_uuid(),
    completed_request_id uuid,
    claimed_request_id uuid,
    lease_token uuid,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error_code text NOT NULL DEFAULT '' CHECK (last_error_code IN ('', 'check_failed')),
    queued_at timestamptz NOT NULL DEFAULT now(),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    checked_at timestamptz,
    capture_sha256 text NOT NULL DEFAULT '' CHECK (capture_sha256 = '' OR capture_sha256 ~ '^[0-9a-f]{64}$'),
    capture_truncated boolean NOT NULL DEFAULT false,
    latest_check jsonb CHECK (latest_check IS NULL OR (jsonb_typeof(latest_check) = 'object' AND COALESCE(latest_check->>'version', '') = '1' AND octet_length(latest_check::text) <= 33554432)),
    CHECK ((lease_token IS NULL AND lease_until IS NULL AND claimed_request_id IS NULL) OR (lease_token IS NOT NULL AND lease_until IS NOT NULL AND claimed_request_id IS NOT NULL)),
    CHECK ((latest_check IS NULL AND checked_at IS NULL AND completed_request_id IS NULL) OR (latest_check IS NOT NULL AND checked_at IS NOT NULL AND completed_request_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS automatic_route_checks_ready_idx ON automatic_route_checks (next_attempt_at, queued_at, deployment_id) WHERE completed_request_id IS DISTINCT FROM request_id;
CREATE INDEX IF NOT EXISTS automatic_route_checks_app_idx ON automatic_route_checks (app_id);

-- Capture/intent writes and their queue handoff commit together. Explicit
-- refresh coalesces with already pending work; changed inputs supersede leases.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_automatic_route_check(p_app uuid, p_deployment uuid DEFAULT NULL, p_force boolean DEFAULT true) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO automatic_route_checks (deployment_id, app_id, account_id)
    SELECT d.id, a.id, a.account_id FROM deployments d
    JOIN apps a ON a.id = d.app_id
    JOIN saved_route_requirements s ON s.app_id = a.id AND s.account_id = a.account_id
    WHERE a.id = p_app AND a.status <> 'deleted'
      AND (p_deployment IS NULL OR d.id = p_deployment)
      AND (p_deployment IS NOT NULL OR EXISTS (SELECT 1 FROM deployment_openapi_docs c WHERE c.deployment_id = d.id AND c.app_id = a.id AND c.account_id = a.account_id) OR EXISTS (SELECT 1 FROM automatic_route_checks j WHERE j.deployment_id = d.id))
    ORDER BY d.id
    ON CONFLICT (deployment_id) DO UPDATE SET
        request_id = gen_random_uuid(), queued_at = now(), next_attempt_at = now(),
        attempts = 0, last_error_code = '', lease_token = NULL, lease_until = NULL, claimed_request_id = NULL
    WHERE p_force OR automatic_route_checks.completed_request_id = automatic_route_checks.request_id OR automatic_route_checks.last_error_code <> '';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION automatic_route_check_capture_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM enqueue_automatic_route_check(OLD.app_id, OLD.deployment_id);
        RETURN OLD;
    END IF;
    IF TG_OP = 'INSERT' OR NEW.doc_sha256 IS DISTINCT FROM OLD.doc_sha256 OR NEW.truncated IS DISTINCT FROM OLD.truncated THEN
        PERFORM enqueue_automatic_route_check(NEW.app_id, NEW.deployment_id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS automatic_route_check_capture_changed ON deployment_openapi_docs;
CREATE TRIGGER automatic_route_check_capture_changed AFTER INSERT OR UPDATE OR DELETE ON deployment_openapi_docs FOR EACH ROW EXECUTE FUNCTION automatic_route_check_capture_changed();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION automatic_route_check_intent_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.revision IS DISTINCT FROM OLD.revision OR NEW.sha256 IS DISTINCT FROM OLD.sha256 THEN
        PERFORM enqueue_automatic_route_check(NEW.app_id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS automatic_route_check_intent_changed ON saved_route_requirements;
CREATE TRIGGER automatic_route_check_intent_changed AFTER INSERT OR UPDATE ON saved_route_requirements FOR EACH ROW EXECUTE FUNCTION automatic_route_check_intent_changed();
-- Existing saved intent receives an initial check for each retained capture.
SELECT enqueue_automatic_route_check(app_id) FROM saved_route_requirements;

-- +goose Down
DROP TRIGGER automatic_route_check_intent_changed ON saved_route_requirements;
DROP TRIGGER automatic_route_check_capture_changed ON deployment_openapi_docs;
DROP FUNCTION automatic_route_check_intent_changed();
DROP FUNCTION automatic_route_check_capture_changed();
DROP FUNCTION enqueue_automatic_route_check(uuid, uuid, boolean);
DROP TABLE automatic_route_checks;
