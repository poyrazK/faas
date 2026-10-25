-- filename: 20261026100000000_runtime_generation_on_secret_reseal.sql
-- ADR-1741: schedd's owner cache keeps each app's runtime values, including
-- the sealed project-environment secrets and the deployment's pinned release
-- secret snapshot, until the app's runtime generation moves. A value change
-- moves it through the runtime-configuration change it stamps, but a reseal
-- (master-key rotation, account binding) rewrites only the ciphertext and
-- deliberately stamps no change (ADR-1139). The cache then kept handing wakes
-- the pre-reseal envelope: one sealed to a key being retired, or unbound when
-- vmmd requires bound envelopes. These triggers move the generation, and
-- nothing else, when a reseal rewrites either table's ciphertext.
--
-- The previous release neither reads these triggers nor depends on their
-- absence; a bump only drops a cache entry.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Project-environment secrets are shared by every app of the project's
-- environment; a reseal is rare, so every app of the project moves.
CREATE OR REPLACE FUNCTION app_runtime_generation_on_project_environment_secrets() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE member uuid;
BEGIN
    FOR member IN SELECT id FROM apps WHERE project_id = NEW.project_id ORDER BY id LOOP
        PERFORM app_runtime_generation_bump(member);
    END LOOP;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS app_runtime_generation_project_environment_secrets ON project_environment_secrets;
CREATE TRIGGER app_runtime_generation_project_environment_secrets AFTER UPDATE ON project_environment_secrets
    FOR EACH ROW WHEN (OLD.ciphertext IS DISTINCT FROM NEW.ciphertext OR OLD.kid IS DISTINCT FROM NEW.kid)
    EXECUTE FUNCTION app_runtime_generation_on_project_environment_secrets();

DROP TRIGGER IF EXISTS app_runtime_generation_release_secret_snapshots ON deployment_release_secret_snapshots;
CREATE TRIGGER app_runtime_generation_release_secret_snapshots AFTER UPDATE ON deployment_release_secret_snapshots
    FOR EACH ROW WHEN (OLD.values_json IS DISTINCT FROM NEW.values_json)
    EXECUTE FUNCTION app_runtime_generation_on_deployment_row();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_runtime_generation_release_secret_snapshots ON deployment_release_secret_snapshots;
DROP TRIGGER IF EXISTS app_runtime_generation_project_environment_secrets ON project_environment_secrets;
DROP FUNCTION IF EXISTS app_runtime_generation_on_project_environment_secrets();
-- +goose StatementEnd
