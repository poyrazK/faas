-- filename: 20261024230705892_rehearsal_webhook_fence_insert_lock.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- scenario_webhook_delivery_fence runs on every write to app_webhook_deliveries
-- for every app (ADR-20261024230731). It locked the delivery's apps rows on
-- every claim and completion, so one app held FOR UPDATE stalled a claim batch
-- that spans other accounts. Only an insert can race a registration: the
-- registration takes FOR UPDATE and refuses an app that already has deliveries,
-- so an existing row's app can never gain a policy. Updates now take no lock
-- and read the policy only when the row would become runnable. Replacing the
-- function takes no table lock.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_webhook_delivery_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Serialize new deliveries with the registration transaction. A registration
    -- takes FOR UPDATE and refuses any app that already has work or deliveries.
    IF TG_OP = 'INSERT' THEN
        PERFORM id FROM apps
        WHERE id = NEW.app_id OR id = (SELECT app_id FROM app_webhooks WHERE id = NEW.webhook_id)
        ORDER BY id FOR KEY SHARE;
    END IF;
    IF NEW.status IN ('pending', 'in_flight', 'failed') THEN
        IF EXISTS (
            SELECT 1 FROM scenario_test_action_policies p
            WHERE p.app_id = NEW.app_id
               OR p.app_id = (SELECT app_id FROM app_webhooks WHERE id = NEW.webhook_id)
        ) THEN
            NEW.status := 'dead';
            NEW.last_error := 'rehearsal: unsupported webhook delivery suppressed';
            NEW.updated_at := clock_timestamp();
        END IF;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_webhook_delivery_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Serialize new deliveries with the registration transaction. A registration
    -- takes FOR UPDATE and refuses any app that already has work or deliveries.
    PERFORM id FROM apps
    WHERE id = NEW.app_id OR id = (SELECT app_id FROM app_webhooks WHERE id = NEW.webhook_id)
    ORDER BY id FOR KEY SHARE;
    IF EXISTS (
        SELECT 1 FROM scenario_test_action_policies p
        WHERE p.app_id = NEW.app_id
           OR p.app_id = (SELECT app_id FROM app_webhooks WHERE id = NEW.webhook_id)
    ) AND NEW.status IN ('pending', 'in_flight', 'failed') THEN
        NEW.status := 'dead';
        NEW.last_error := 'rehearsal: unsupported webhook delivery suppressed';
        NEW.updated_at := clock_timestamp();
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
