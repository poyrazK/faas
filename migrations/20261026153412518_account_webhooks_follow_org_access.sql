-- filename: 20261026153412518_account_webhooks_follow_org_access.sql
-- migration-compat: expand
-- ADR-20261026151103: an account-scope release webhook fanned out the
-- deployments of every app carrying its account, and a company app carries
-- its creator's account, so a creator who left the company kept receiving
-- the company apps' deployment and rollout events. The fan-out now takes
-- only the apps of organizations the webhook's account may still see:
-- personal apps, and company apps while the account is an active member.
-- GitOps notifications use the same rule (EnqueueEnvironmentGitNotification).
--
-- The previous release fires the same triggers; it only sees fewer
-- deliveries for departed members.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION account_may_see_org_resources(p_account uuid, p_org uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    SELECT p_org IS NULL OR EXISTS (
        SELECT 1 FROM orgs o
         WHERE o.id = p_org
           AND (o.personal_org
                OR (NOT o.deleted_pending AND o.status <> 'deleted_pending'
                    AND EXISTS (SELECT 1 FROM org_memberships m
                                 WHERE m.org_id = o.id AND m.account_id = p_account AND m.removed_at IS NULL
                                   AND m.role IN ('owner', 'admin', 'developer', 'viewer')))))
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_deployment_lifecycle_webhooks() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    event_name text;
    event_payload jsonb;
BEGIN
    IF NEW.status = OLD.status OR NEW.status NOT IN ('live', 'failed') THEN
        RETURN NEW;
    END IF;

    event_name := 'deployment.' || NEW.status;
    event_payload := jsonb_build_object(
        'app_id', NEW.app_id,
        'deployment_id', NEW.id,
        'status', NEW.status
    );
    IF NEW.status = 'failed' THEN
        event_payload := event_payload || jsonb_strip_nulls(jsonb_build_object(
            'error_code', NEW.error_code,
            'error_hint', NEW.error_hint,
            'error_why', NEW.error_why,
            'error_fix', NEW.error_fix
        ));
    END IF;

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.enabled
       AND ((h.scope = 'app' AND h.app_id = NEW.app_id)
            OR (h.scope = 'account' AND h.app_id IS NULL
                AND account_may_see_org_resources(h.account_id, a.org_id)))
       AND (event_name = ANY(h.event_filter)
            OR (h.scope = 'app' AND cardinality(h.event_filter) = 0));
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_rollout_outcome_webhooks() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    event_name text;
    event_payload jsonb;
BEGIN
    IF NEW.rollout_state IS NOT DISTINCT FROM OLD.rollout_state
       OR OLD.rollout_state NOT IN ('pending', 'rolling_out') THEN
        RETURN NEW;
    END IF;

    IF NEW.rollout_state = 'complete' AND NEW.status = 'live' THEN
        event_name := 'rollout.completed';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'complete',
            'traffic_percent', NEW.traffic_percent,
            'completed_at', COALESCE(NEW.rollout_completed_at, now())
        ) || jsonb_strip_nulls(jsonb_build_object('decision',
            CASE WHEN NEW.rollout_decision->>'kind' = 'advance' THEN NEW.rollout_decision END));
    ELSIF NEW.rollout_state = 'aborted' AND OLD.status = 'live'
          AND NEW.status IN ('live', 'superseded') THEN
        event_name := 'rollout.aborted';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'aborted',
            'traffic_percent', NEW.traffic_percent,
            'reason', COALESCE(NULLIF(NEW.rollout_aborted_reason, ''), 'rollout aborted'),
            'aborted_at', COALESCE(NEW.rollout_aborted_at, now())
        ) || jsonb_strip_nulls(jsonb_build_object('decision',
            CASE WHEN NEW.rollout_decision->>'kind' = 'abort' THEN NEW.rollout_decision END));
    ELSE
        RETURN NEW;
    END IF;

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.enabled
       AND ((h.scope = 'app' AND h.app_id = NEW.app_id)
            OR (h.scope = 'account' AND h.app_id IS NULL
                AND account_may_see_org_resources(h.account_id, a.org_id)))
       AND (event_name = ANY(h.event_filter)
            OR (h.scope = 'app' AND cardinality(h.event_filter) = 0));
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- The functions keep the visibility check: the previous release's behaviour
-- without it leaked company events to departed members.
SELECT 1;
