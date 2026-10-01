-- +goose Up
-- A signer/drain UPDATE or DELETE already owns its child row before a BEFORE
-- trigger runs. Use a shared control fence instead of waiting back on the app
-- row, preserving legacy app-first deletion/mutation lock ordering.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_control_input_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN app_ids := array_append(app_ids, OLD.app_id); END IF;
    IF TG_OP <> 'DELETE' THEN app_ids := array_append(app_ids, NEW.app_id); END IF;
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.' || id::text, 0))
    FROM (SELECT DISTINCT id FROM unnest(app_ids) id WHERE id IS NOT NULL ORDER BY id) controls;
    IF TG_NARGS > 0 AND TG_ARGV[0] = 'account_quota' THEN
        PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.account-quota.' || account_id::text, 0))
        FROM (SELECT DISTINCT account_id FROM apps WHERE id = ANY(app_ids) ORDER BY account_id) owners;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Billing attribution is fixed for an app identity. Organization/project
-- moves use their existing reenrollment path; another creating account clones
-- into a new app. This also keeps quota ownership stable during a child write.
-- +goose StatementBegin
CREATE FUNCTION application_standard_app_account_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'application creating account identity is immutable'
        USING ERRCODE = '23514', CONSTRAINT = 'application_standard_app_account_identity';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_app_account_identity_guard
BEFORE UPDATE OF account_id ON apps FOR EACH ROW
WHEN (OLD.account_id IS DISTINCT FROM NEW.account_id)
EXECUTE FUNCTION application_standard_app_account_identity_guard();

-- +goose Down
DROP TRIGGER application_standard_app_account_identity_guard ON apps;
DROP FUNCTION application_standard_app_account_identity_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_control_input_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN app_ids := array_append(app_ids, OLD.app_id); END IF;
    IF TG_OP <> 'DELETE' THEN app_ids := array_append(app_ids, NEW.app_id); END IF;
    PERFORM 1 FROM apps WHERE id = ANY(app_ids) ORDER BY id FOR SHARE;
    IF TG_NARGS > 0 AND TG_ARGV[0] = 'account_quota' THEN
        PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.account-quota.' || account_id::text, 0))
        FROM (SELECT DISTINCT account_id FROM apps WHERE id = ANY(app_ids) ORDER BY account_id) owners;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
