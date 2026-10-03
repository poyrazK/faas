-- +goose Up
ALTER TABLE automatic_route_checks ADD COLUMN IF NOT EXISTS safety_state text NOT NULL DEFAULT 'unknown'
    CHECK (safety_state IN ('unknown', 'satisfied', 'violated'));
-- Keep the final route event vocabulary during replay so retained events
-- written by later migrations remain valid.
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
    CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken', 'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached', 'routes.requirements.violated', 'routes.requirements.recovered', 'routes.requirements.changed', 'routes.health.blocked', 'routes.health.resumed', 'routes.health.aborted'));

-- Only currently live deployments participate in policy monitoring. Retained
-- historical captures remain available for explicit checks, without alerts.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_route_policy_checks(p_app uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE target uuid;
BEGIN
    FOR target IN SELECT d.id FROM deployments d JOIN apps a ON a.id = d.app_id
        JOIN saved_route_requirements s ON s.app_id = a.id AND s.account_id = a.account_id
        WHERE a.id = p_app AND a.status <> 'deleted' AND d.status = 'live'
          AND (EXISTS (SELECT 1 FROM deployment_openapi_docs c WHERE c.deployment_id = d.id AND c.app_id = a.id AND c.account_id = a.account_id)
            OR EXISTS (SELECT 1 FROM automatic_route_checks j WHERE j.deployment_id = d.id))
        ORDER BY d.id
    LOOP
        PERFORM enqueue_automatic_route_check(p_app, target, true);
    END LOOP;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION route_policy_rule_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM enqueue_route_policy_checks(OLD.app_id);
        RETURN OLD;
    END IF;
    IF TG_OP = 'INSERT' OR ROW(NEW.app_id, NEW.account_id, NEW.match_host, NEW.match_path, NEW.match_methods, NEW.match_headers, NEW.priority, NEW.enabled, NEW.kind, NEW.action, NEW.validate_mode)
        IS DISTINCT FROM ROW(OLD.app_id, OLD.account_id, OLD.match_host, OLD.match_path, OLD.match_methods, OLD.match_headers, OLD.priority, OLD.enabled, OLD.kind, OLD.action, OLD.validate_mode) THEN
        IF TG_OP = 'UPDATE' AND NEW.app_id IS DISTINCT FROM OLD.app_id THEN
            PERFORM enqueue_route_policy_checks(OLD.app_id);
        END IF;
        PERFORM enqueue_route_policy_checks(NEW.app_id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS route_policy_rule_changed ON edge_rules;
CREATE TRIGGER route_policy_rule_changed AFTER INSERT OR UPDATE OR DELETE ON edge_rules FOR EACH ROW EXECUTE FUNCTION route_policy_rule_changed();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION route_policy_app_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.slug, NEW.type, NEW.consumer_auth_mode, NEW.maintenance_mode, NEW.manifest->'request_timeout_s', NEW.request_rate_limit_rps, NEW.request_rate_limit_burst, NEW.ram_mb, NEW.cpu_millicores, NEW.max_concurrency, NEW.scaling_policy)
        IS DISTINCT FROM ROW(OLD.slug, OLD.type, OLD.consumer_auth_mode, OLD.maintenance_mode, OLD.manifest->'request_timeout_s', OLD.request_rate_limit_rps, OLD.request_rate_limit_burst, OLD.ram_mb, OLD.cpu_millicores, OLD.max_concurrency, OLD.scaling_policy) THEN
        PERFORM enqueue_route_policy_checks(NEW.id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS route_policy_app_changed ON apps;
CREATE TRIGGER route_policy_app_changed AFTER UPDATE ON apps FOR EACH ROW EXECUTE FUNCTION route_policy_app_changed();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION route_policy_account_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target uuid;
BEGIN
    IF ROW(NEW.plan, NEW.status, NEW.abuse_hold_at IS NULL) IS DISTINCT FROM ROW(OLD.plan, OLD.status, OLD.abuse_hold_at IS NULL) THEN
        FOR target IN SELECT id FROM apps WHERE account_id = NEW.id AND status <> 'deleted' ORDER BY id LOOP
            PERFORM enqueue_route_policy_checks(target);
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS route_policy_account_changed ON accounts;
CREATE TRIGGER route_policy_account_changed AFTER UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION route_policy_account_changed();
-- A previously captured deployment that becomes live needs a current check.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION route_policy_deployment_live() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'live' AND OLD.status IS DISTINCT FROM NEW.status THEN
        PERFORM enqueue_route_policy_checks(NEW.app_id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS route_policy_deployment_live ON deployments;
CREATE TRIGGER route_policy_deployment_live AFTER UPDATE OF status ON deployments FOR EACH ROW EXECUTE FUNCTION route_policy_deployment_live();

-- Completion and notification intent commit together. Unknown evidence never
-- clears a confirmed violation. Capture recipients at the transition, so a
-- later subscriber cannot receive private historical events.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION route_requirements_safety_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE event_name text; recipients uuid[]; event_payload jsonb; app_slug text;
BEGIN
    IF NEW.safety_state = 'violated' THEN
        event_name := 'routes.requirements.violated';
    ELSIF NEW.safety_state = 'satisfied' AND OLD.safety_state = 'violated' THEN
        event_name := 'routes.requirements.recovered';
    ELSE
        RETURN NEW;
    END IF;
    SELECT a.slug INTO app_slug FROM apps a JOIN deployments d ON d.app_id = a.id
        WHERE a.id = NEW.app_id AND a.account_id = NEW.account_id AND a.status <> 'deleted'
          AND d.id = NEW.deployment_id AND d.status = 'live';
    IF NOT FOUND THEN RETURN NEW; END IF;
    SELECT array_agg(h.id ORDER BY h.id) INTO recipients FROM app_webhooks h
        WHERE h.scope = 'app' AND h.app_id = NEW.app_id AND h.account_id = NEW.account_id AND h.enabled
          AND (cardinality(h.event_filter) = 0 OR event_name = ANY(h.event_filter));
    IF recipients IS NULL THEN RETURN NEW; END IF;
    event_payload := jsonb_build_object(
        'app_id', NEW.app_id, 'deployment_id', NEW.deployment_id,
        'status', NEW.safety_state, 'previous_status', OLD.safety_state,
        'transition_id', NEW.completed_request_id, 'checked_at', NEW.checked_at,
        'requirements_revision', NEW.latest_check->'requirements_revision',
        'requirements_sha256', NEW.latest_check->>'requirements_sha256',
        'configuration_sha256', NEW.latest_check->>'configuration_sha256',
        'capture_sha256', NEW.capture_sha256,
        'result_path', '/v1/apps/' || app_slug || '/route-requirements/checks/' || NEW.deployment_id::text);
    INSERT INTO app_webhook_event_outbox(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
        VALUES (NEW.account_id, NEW.app_id, event_name, NEW.completed_request_id, event_payload, recipients)
        ON CONFLICT (event, source_id) DO NOTHING;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS route_requirements_safety_changed ON automatic_route_checks;
CREATE TRIGGER route_requirements_safety_changed AFTER UPDATE OF safety_state ON automatic_route_checks
    FOR EACH ROW WHEN (OLD.safety_state IS DISTINCT FROM NEW.safety_state) EXECUTE FUNCTION route_requirements_safety_changed();
SELECT enqueue_route_policy_checks(app_id) FROM saved_route_requirements ORDER BY app_id;

-- +goose Down
-- Forward-only: keep notification intent, confirmed state and replayable history.
SELECT 1;
