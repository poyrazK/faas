-- filename: 20261102071817365_operator_actions_reach_audit_log.sql
-- migration-compat: expand
-- ADR-20261102071817: operator actions reach audit_log. Every operator.action.*
-- row (views, drains, force parks, cancels and their outcomes) is written to
-- events by apid, schedd and the audit outbox, but the operator audit search,
-- the overview's 24-hour count and the anchors read audit_log, which only
-- account deletions and alert triage wrote. An AFTER INSERT trigger on events
-- now copies each operator.action.* row into audit_log in the same
-- transaction, under the operator who acted (data.actor, else
-- events.actor_account_id) as alert triage already does, so the customer's
-- own audit log, filtered to their account, does not show it. The payload is
-- kept verbatim, with events.trace_id added as data.trace_id when the payload
-- has none. Rows written before this migration are not copied: their
-- anchor windows are sealed.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION events_copy_operator_action_to_audit_log() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    operator_id uuid := NEW.actor_account_id;
    payload jsonb := coalesce(NEW.data, '{}'::jsonb);
BEGIN
    IF payload->>'actor' ~* '^[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}$' THEN
        operator_id := (payload->>'actor')::uuid;
    END IF;
    IF NEW.trace_id IS NOT NULL AND jsonb_typeof(payload) = 'object' AND NOT payload ? 'trace_id' THEN
        payload := payload || jsonb_build_object('trace_id', NEW.trace_id);
    END IF;
    INSERT INTO audit_log (id, kind, account_id, account_email, actor, received_at, data)
    VALUES (gen_random_uuid(), NEW.kind, operator_id,
            (SELECT a.email FROM accounts a WHERE a.id = operator_id),
            coalesce(payload->>'actor', NEW.actor), NEW.at, payload);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS events_operator_action_audit_log ON events;
CREATE TRIGGER events_operator_action_audit_log AFTER INSERT ON events
    FOR EACH ROW WHEN (NEW.kind LIKE 'operator.action.%')
    EXECUTE FUNCTION events_copy_operator_action_to_audit_log();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS events_operator_action_audit_log ON events;
DROP FUNCTION IF EXISTS events_copy_operator_action_to_audit_log();
