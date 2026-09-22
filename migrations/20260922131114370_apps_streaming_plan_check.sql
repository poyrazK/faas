-- +goose Up
-- +goose StatementBegin

-- ADR-102 follow-up: keep the app-level streaming opt-in aligned with the
-- owning account's entitlement. `apps` intentionally stores only
-- `account_id`, so PostgreSQL cannot express this relation with a subquery
-- directly in a CHECK constraint. The stable helper gives the constraint a
-- row-local expression while the account trigger closes the other mutation
-- direction (a paid -> Free downgrade).
CREATE OR REPLACE FUNCTION apps_streaming_plan_allowed(p_account_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE
AS $function$
    SELECT EXISTS (
        SELECT 1
          FROM accounts
         WHERE id = p_account_id
           AND plan <> 'free'
    );
$function$;

CREATE OR REPLACE FUNCTION apps_streaming_plan_account_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF NEW.plan = 'free'
       AND OLD.plan IS DISTINCT FROM NEW.plan
       AND EXISTS (
           SELECT 1
             FROM apps
            WHERE account_id = NEW.id
              AND streaming_enabled
       ) THEN
        RAISE EXCEPTION
            'account % cannot downgrade to Free while a streaming-enabled app exists',
            NEW.id
            USING ERRCODE = '23514',
                  CONSTRAINT = 'apps_streaming_enabled_plan_check';
    END IF;
    RETURN NEW;
END;
$function$;

-- Lock the account before an app opts in. Account updates already hold that
-- row lock before their constraint trigger runs, so a concurrent opt-in and
-- downgrade serialize instead of both passing a snapshot-based CHECK.
CREATE OR REPLACE FUNCTION apps_streaming_plan_app_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF NEW.streaming_enabled THEN
        PERFORM 1
          FROM accounts
         WHERE id = NEW.account_id
         FOR UPDATE;
    END IF;
    RETURN NEW;
END;
$function$;

DROP TRIGGER IF EXISTS apps_streaming_plan_app_guard_trg ON apps;
CREATE TRIGGER apps_streaming_plan_app_guard_trg
    BEFORE INSERT OR UPDATE OF account_id, streaming_enabled ON apps
    FOR EACH ROW
    EXECUTE FUNCTION apps_streaming_plan_app_guard();

DROP TRIGGER IF EXISTS apps_streaming_plan_account_guard_trg ON accounts;
CREATE CONSTRAINT TRIGGER apps_streaming_plan_account_guard_trg
    AFTER UPDATE OF plan ON accounts
    DEFERRABLE INITIALLY IMMEDIATE
    FOR EACH ROW
    EXECUTE FUNCTION apps_streaming_plan_account_guard();

ALTER TABLE apps
    DROP CONSTRAINT IF EXISTS apps_streaming_enabled_plan_check;
ALTER TABLE apps
    ADD CONSTRAINT apps_streaming_enabled_plan_check
    CHECK (NOT streaming_enabled OR apps_streaming_plan_allowed(account_id))
    NOT VALID;
ALTER TABLE apps
    VALIDATE CONSTRAINT apps_streaming_enabled_plan_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE apps
    DROP CONSTRAINT IF EXISTS apps_streaming_enabled_plan_check;
DROP TRIGGER IF EXISTS apps_streaming_plan_account_guard_trg ON accounts;
DROP TRIGGER IF EXISTS apps_streaming_plan_app_guard_trg ON apps;
DROP FUNCTION IF EXISTS apps_streaming_plan_account_guard();
DROP FUNCTION IF EXISTS apps_streaming_plan_app_guard();
DROP FUNCTION IF EXISTS apps_streaming_plan_allowed(uuid);

-- +goose StatementEnd
