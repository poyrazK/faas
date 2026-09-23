-- filename: 20260923163517663_deployment_companion_capacity.sql

-- +goose Up
-- +goose StatementBegin

-- Expand the sidecar set from one init + one long-running companion to one
-- init + four concurrently running companions (five helper entries total).
-- Keep the JSON deployment record and normalized filesystem-handle table in
-- lockstep so hand-written SQL cannot bypass the same bounded cardinality.
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_sidecars_cap_chk;

ALTER TABLE deployments
    ADD CONSTRAINT deployments_sidecars_cap_chk
        CHECK (jsonb_array_length(sidecars) <= 5);

CREATE OR REPLACE FUNCTION deployment_sidecar_layers_cap_check()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_count integer;
BEGIN
    -- Excluding NEW.sidecar_name makes an upsert of an existing layer safe
    -- even when the deployment already has the maximum number of helpers.
    SELECT count(*) INTO current_count
        FROM deployment_sidecar_layers
        WHERE deployment_id = NEW.deployment_id
          AND sidecar_name <> NEW.sidecar_name;

    IF current_count >= 5 THEN
        RAISE EXCEPTION 'deployment_sidecar_layers: deployment % exceeds the 5-row cap (existing other rows=%, new would exceed 5)',
            NEW.deployment_id, current_count
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM deployments WHERE jsonb_array_length(sidecars) > 2
    ) OR EXISTS (
        SELECT 1 FROM deployment_sidecar_layers
        GROUP BY deployment_id HAVING count(*) > 2
    ) THEN
        RAISE EXCEPTION 'cannot restore the two-helper cap while deployments contain more than two helpers';
    END IF;
END $$;

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_sidecars_cap_chk;

ALTER TABLE deployments
    ADD CONSTRAINT deployments_sidecars_cap_chk
        CHECK (jsonb_array_length(sidecars) <= 2);

CREATE OR REPLACE FUNCTION deployment_sidecar_layers_cap_check()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_count integer;
BEGIN
    SELECT count(*) INTO current_count
        FROM deployment_sidecar_layers
        WHERE deployment_id = NEW.deployment_id
          AND sidecar_name <> NEW.sidecar_name;

    IF current_count >= 2 THEN
        RAISE EXCEPTION 'deployment_sidecar_layers: deployment % exceeds the 2-row cap (existing other rows=%, new would exceed 2)',
            NEW.deployment_id, current_count
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- +goose StatementEnd
