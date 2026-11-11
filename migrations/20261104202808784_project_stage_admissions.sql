-- filename: 20261104202808784_project_stage_admissions.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A shared app's stage origin outlives its final environment. A production
-- head, deployment or accepted source still admits it to production normally.
CREATE TABLE IF NOT EXISTS project_app_stage_admissions (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS project_app_stage_admissions_project_id_idx ON project_app_stage_admissions(project_id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remember_project_app_stage_admission() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO project_app_stage_admissions(app_id, project_id)
    SELECT a.id, e.project_id FROM project_environments e
    JOIN apps a ON a.id = NEW.app_id AND a.project_id = e.project_id
    WHERE e.id = NEW.environment_id AND e.slug <> 'production'
    ON CONFLICT(app_id) DO UPDATE SET project_id = excluded.project_id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER project_app_stage_admission
    AFTER INSERT OR UPDATE ON project_environment_workload_heads
    FOR EACH ROW EXECUTE FUNCTION remember_project_app_stage_admission();

-- Install the hook before taking the initial snapshot. Its table lock keeps
-- N-1 head creation and deletion from slipping between capture and tracking.
INSERT INTO project_app_stage_admissions(app_id, project_id)
SELECT DISTINCT a.id, a.project_id FROM project_environment_workload_heads h
JOIN project_environments e ON e.id = h.environment_id
JOIN apps a ON a.id = h.app_id AND a.project_id = e.project_id
WHERE e.slug <> 'production'
ON CONFLICT(app_id) DO UPDATE SET project_id = excluded.project_id;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_release_environment_apps(target_project uuid, target_environment text)
RETURNS SETOF apps LANGUAGE sql STABLE AS $members$
 SELECT a.* FROM apps a
 WHERE a.project_id=target_project AND a.status<>'deleted' AND coalesce(a.preview_of_slug,'')=''
 AND CASE WHEN target_environment IN ('', 'default', 'production') THEN NOT (
   coalesce(a.manifest->>'project_source_sha256', '')=''
   AND (EXISTS (SELECT 1 FROM project_app_stage_admissions origin
     WHERE origin.app_id=a.id AND origin.project_id=a.project_id)
     OR EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug<>'production'))
   AND NOT EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug='production')
   AND NOT EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope IN ('', 'default', 'production'))
 ) ELSE (
   EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug=target_environment)
   OR EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=target_environment AND d.deleted_at IS NULL)
 ) END
$members$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_release_environment_apps(target_project uuid, target_environment text)
RETURNS SETOF apps LANGUAGE sql STABLE AS $members$
 SELECT a.* FROM apps a
 WHERE a.project_id=target_project AND a.status<>'deleted' AND coalesce(a.preview_of_slug,'')=''
 AND CASE WHEN target_environment IN ('', 'default', 'production') THEN NOT (
   coalesce(a.manifest->>'project_source_sha256', '')=''
   AND EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug<>'production')
   AND NOT EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug='production')
   AND NOT EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope IN ('', 'default', 'production'))
 ) ELSE (
   EXISTS (SELECT 1 FROM project_environment_workload_heads h
     JOIN project_environments e ON e.id=h.environment_id
     WHERE h.app_id=a.id AND e.project_id=a.project_id AND e.slug=target_environment)
   OR EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=target_environment)
 ) END
$members$;
-- +goose StatementEnd
DROP TRIGGER project_app_stage_admission ON project_environment_workload_heads;
DROP FUNCTION remember_project_app_stage_admission();
DROP TABLE project_app_stage_admissions;
