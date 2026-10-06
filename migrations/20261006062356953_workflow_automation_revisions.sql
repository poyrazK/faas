-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_automation_revisions (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name)>0),
 version bigint NOT NULL CHECK(version>0),
 definition jsonb NOT NULL CHECK(jsonb_typeof(definition)='object' AND definition->>'name'=name),
 recorded_at timestamptz NOT NULL DEFAULT now(),
 legacy_snapshot boolean NOT NULL DEFAULT false,
 published_by_account_id uuid NOT NULL,
 published_by_api_key_id uuid,
 PRIMARY KEY(app_id,name,version)
);

-- Keep the latest publication that existed before history was introduced.
-- Its original publish time and earlier definitions were not retained, so mark
-- this snapshot explicitly and timestamp when the history record was created.
INSERT INTO workflow_automation_revisions(
 app_id,name,version,definition,recorded_at,legacy_snapshot,published_by_account_id
)
SELECT d.app_id,d.name,d.published_version,d.published,now(),true,a.account_id
FROM workflow_automation_definitions d
JOIN apps a ON a.id=d.app_id
WHERE d.published IS NOT NULL AND d.published_version>0
ON CONFLICT(app_id,name,version) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS workflow_automation_revisions;
