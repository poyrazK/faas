-- filename: 20261114032236740_billing_plan_activation_trigger_schema.sql
-- migration-compat: expand
-- retain_billing_plan_activation (20261111155713137) wrote to
-- public.billing_plan_activations, so a plan change in any other schema (an
-- isolated test schema, a restore rehearsal) failed with "relation does not
-- exist". It now writes beside the row that changed, as the app owner
-- authority trigger does. Production runs in public, where nothing changes.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_billing_plan_activation() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF NEW.plan IS DISTINCT FROM OLD.plan THEN
        EXECUTE format('INSERT INTO %I.billing_plan_activations(subject_id, from_plan, to_plan, effective_at) '
                       'VALUES ($1, $2, $3, $4)', TG_TABLE_SCHEMA)
            USING NEW.id, OLD.plan, NEW.plan, NEW.plan_changed_at;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION retain_billing_plan_activation() FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_billing_plan_activation() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.plan IS DISTINCT FROM OLD.plan THEN
        INSERT INTO public.billing_plan_activations(subject_id, from_plan, to_plan, effective_at)
        VALUES (NEW.id, OLD.plan, NEW.plan, NEW.plan_changed_at);
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION retain_billing_plan_activation() FROM PUBLIC;
