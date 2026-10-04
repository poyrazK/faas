-- +goose Up
-- Keep the original environment lifetime after its specs and pins cascade away.
-- Deliberately no environment FK: deleting/recreating a slug must not grant an
-- old VM access to the replacement environment. Deployment retention owns GC.
CREATE TABLE deployment_runtime_environment_owners (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL CHECK (environment_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

INSERT INTO deployment_runtime_environment_owners (deployment_id, environment_id)
SELECT p.deployment_id, s.environment_id
FROM project_environment_workload_deployment_specs p
JOIN project_environment_workload_specs s ON s.id=p.spec_id;

-- +goose StatementBegin
CREATE FUNCTION bind_deployment_runtime_environment() RETURNS trigger AS $$
DECLARE
    selected_environment uuid;
    original_environment uuid;
BEGIN
    SELECT s.environment_id INTO STRICT selected_environment
    FROM project_environment_workload_specs s
    JOIN project_environments e ON e.id=s.environment_id
    JOIN apps a ON a.id=s.app_id AND a.account_id=e.account_id AND a.project_id=e.project_id
    JOIN deployments d ON d.app_id=a.id AND d.id=NEW.deployment_id
    WHERE s.id=NEW.spec_id AND e.slug=CASE WHEN d.scope='default' THEN 'production' ELSE d.scope END;
    INSERT INTO deployment_runtime_environment_owners (deployment_id, environment_id)
    VALUES (NEW.deployment_id, selected_environment)
    ON CONFLICT (deployment_id) DO NOTHING;
    SELECT environment_id INTO STRICT original_environment
    FROM deployment_runtime_environment_owners WHERE deployment_id=NEW.deployment_id;
    IF original_environment <> selected_environment THEN
        RAISE EXCEPTION 'deployment runtime environment is immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER deployment_runtime_environment_bound
AFTER INSERT OR UPDATE ON project_environment_workload_deployment_specs
FOR EACH ROW EXECUTE FUNCTION bind_deployment_runtime_environment();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM deployment_runtime_environment_owners) THEN
        RAISE EXCEPTION 'deployment runtime environment owners must be retained until their deployments are removed';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER deployment_runtime_environment_bound ON project_environment_workload_deployment_specs;
DROP FUNCTION bind_deployment_runtime_environment();
DROP TABLE deployment_runtime_environment_owners;
