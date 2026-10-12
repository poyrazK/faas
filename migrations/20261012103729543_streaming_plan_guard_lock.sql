-- +goose Up
-- +goose StatementBegin

-- Production-us hunt #9 (H9-138): apps_streaming_plan_app_guard locked the
-- owning account FOR UPDATE on every app UPDATE whose SET list names
-- streaming_enabled (UpdateApp always does) while the app was streaming
-- enabled. FOR UPDATE conflicts with the FOR KEY SHARE locks that foreign-key
-- checks take, so a per-request insert referencing accounts and apps
-- (request_id_journal) deadlocked against the app update: the insert held the
-- account key-share lock and waited for the app row, the update held the app
-- row and waited for the account. Postgres logged 26 such deadlocks in a day
-- and deploys under traffic failed with "could not update app".
--
-- The guard exists to serialize an app opting in with a concurrent account
-- downgrade to Free (apps_streaming_plan_account_guard). That needs the lock
-- only when the opt-in is new or moves to another account, and FOR NO KEY
-- UPDATE is enough: it conflicts with the downgrade's own row lock (a
-- non-key UPDATE of accounts) but not with foreign-key checks.
CREATE OR REPLACE FUNCTION apps_streaming_plan_app_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF NEW.streaming_enabled
       AND (TG_OP = 'INSERT'
            OR NOT OLD.streaming_enabled
            OR OLD.account_id IS DISTINCT FROM NEW.account_id) THEN
        PERFORM 1
          FROM accounts
         WHERE id = NEW.account_id
         FOR NO KEY UPDATE;
    END IF;
    RETURN NEW;
END;
$function$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

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

-- +goose StatementEnd
