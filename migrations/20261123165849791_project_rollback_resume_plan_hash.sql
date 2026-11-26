-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Keep the resume approval on worker saves. A new resume may replace or clear it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_project_rollback_plan_hash() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt ? 'plan_hash' THEN
        NEW.receipt := NEW.receipt || jsonb_build_object('plan_hash', OLD.receipt->'plan_hash');
    END IF;
    IF OLD.receipt ? 'resume_plan_hash' AND NOT (OLD.status = 'blocked' AND NEW.status = 'running') THEN
        NEW.receipt := NEW.receipt || jsonb_build_object('resume_plan_hash', OLD.receipt->'resume_plan_hash');
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_project_rollback_plan_hash() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt ? 'plan_hash' THEN
        NEW.receipt := NEW.receipt || jsonb_build_object('plan_hash', OLD.receipt->'plan_hash');
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
