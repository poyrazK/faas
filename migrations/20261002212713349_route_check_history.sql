-- +goose Up
ALTER TABLE automatic_route_checks
    ADD COLUMN finding_baseline jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(finding_baseline) = 'object' AND octet_length(finding_baseline::text) <= 33554432),
    ADD COLUMN latest_changes jsonb CHECK (latest_changes IS NULL OR (jsonb_typeof(latest_changes) = 'object' AND octet_length(latest_changes::text) <= 4194304));
CREATE TABLE route_check_history (
    id uuid PRIMARY KEY,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    checked_at timestamptz NOT NULL,
    encoded_bytes integer NOT NULL CHECK (encoded_bytes BETWEEN 1 AND 33554432),
    entry jsonb NOT NULL CHECK (jsonb_typeof(entry) = 'object' AND entry->>'version' = '1' AND octet_length(entry::text) <= 67108864)
);
CREATE INDEX route_check_history_deployment_idx ON route_check_history(deployment_id, checked_at DESC, id DESC);
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
    CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken', 'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached', 'routes.requirements.violated', 'routes.requirements.recovered', 'routes.requirements.changed'));

-- Finding deltas are computed by the bounded shared evaluator. Only comparable
-- new violations during an existing incident notify; aggregate transitions keep
-- their existing events. Recipient snapshot and intent commit with completion.
-- +goose StatementBegin
CREATE FUNCTION route_requirements_findings_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE recipients uuid[]; app_slug text;
BEGIN
    IF OLD.safety_state <> 'violated' OR NEW.latest_changes->>'status' <> 'comparable'
        OR COALESCE((NEW.latest_changes->'summary'->>'newly_violated')::integer, 0) = 0 THEN
        RETURN NEW;
    END IF;
    SELECT a.slug INTO app_slug FROM apps a JOIN deployments d ON d.app_id = a.id
        WHERE a.id = NEW.app_id AND a.account_id = NEW.account_id AND a.status <> 'deleted'
          AND d.id = NEW.deployment_id AND d.status = 'live';
    IF NOT FOUND THEN RETURN NEW; END IF;
    SELECT array_agg(h.id ORDER BY h.id) INTO recipients FROM app_webhooks h
        WHERE h.scope = 'app' AND h.app_id = NEW.app_id AND h.account_id = NEW.account_id AND h.enabled
          AND (cardinality(h.event_filter) = 0 OR 'routes.requirements.changed' = ANY(h.event_filter));
    IF recipients IS NULL THEN RETURN NEW; END IF;
    INSERT INTO app_webhook_event_outbox(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
    VALUES (NEW.account_id, NEW.app_id, 'routes.requirements.changed', NEW.completed_request_id,
        jsonb_build_object('app_id', NEW.app_id, 'deployment_id', NEW.deployment_id,
            'status', NEW.latest_check->'report'->>'status', 'transition_id', NEW.completed_request_id,
            'checked_at', NEW.checked_at, 'requirements_revision', NEW.latest_check->'requirements_revision',
            'requirements_sha256', NEW.latest_check->>'requirements_sha256',
            'configuration_sha256', NEW.latest_check->>'configuration_sha256', 'capture_sha256', NEW.capture_sha256,
            'summary', NEW.latest_changes->'summary', 'comparison_status', NEW.latest_changes->>'status',
            'result_path', '/v1/apps/' || app_slug || '/route-requirements/checks/' || NEW.deployment_id::text,
            'history_path', '/v1/apps/' || app_slug || '/route-requirements/checks/' || NEW.deployment_id::text || '/history/' || NEW.completed_request_id::text), recipients)
    ON CONFLICT (event, source_id) DO NOTHING;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER route_requirements_findings_changed AFTER UPDATE OF completed_request_id ON automatic_route_checks
    FOR EACH ROW WHEN (OLD.completed_request_id IS DISTINCT FROM NEW.completed_request_id) EXECUTE FUNCTION route_requirements_findings_changed();

-- +goose Down
-- Forward-only: preserve retained evidence and durable notification intent.
SELECT 1;
