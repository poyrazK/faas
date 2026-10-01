-- +goose Up
-- ADR-375: persistent ownership survives missing routing headers and fences
-- environment deletion against every admitted stage invocation.
ALTER TABLE invocations ADD COLUMN environment_id uuid REFERENCES project_environments(id);
CREATE INDEX invocations_environment_owner_idx ON invocations(environment_id) WHERE environment_id IS NOT NULL;

UPDATE invocations i SET environment_id=p.environment_id
FROM invocation_work_environment_admissions p WHERE p.invocation_id=i.id;

-- Legacy stage producers stamped canonical pins. Adopt only an owned app,
-- project and registered environment; arbitrary or foreign pins are ignored.
WITH owners AS (
    SELECT i.id,e.id AS environment_id
    FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
    JOIN deployments d ON d.app_id=a.id AND d.id::text=lower(i.headers->>'X-Gregale-Revision')
    JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=d.scope
    WHERE i.environment_id IS NULL AND e.slug NOT IN ('production','default') AND NOT i.headers ? 'X-Gregale-Release'
    UNION ALL
    SELECT i.id,e.id AS environment_id
    FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
    JOIN project_release_sets r ON r.project_id=a.project_id AND r.account_id=a.account_id
        AND r.id::text=lower(i.headers->>'X-Gregale-Release')
    JOIN project_environments e ON e.project_id=r.project_id AND e.account_id=r.account_id AND e.slug=r.environment_slug
    JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id
    JOIN deployments d ON d.id=m.deployment_id AND d.app_id=a.id AND d.scope=e.slug
    WHERE i.environment_id IS NULL AND e.slug NOT IN ('production','default') AND NOT i.headers ? 'X-Gregale-Revision'
)
UPDATE invocations i SET environment_id=o.environment_id FROM owners o WHERE i.id=o.id;

-- +goose Down
ALTER TABLE invocations DROP COLUMN environment_id;
