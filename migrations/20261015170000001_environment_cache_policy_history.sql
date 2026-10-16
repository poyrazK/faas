-- +goose Up
CREATE TABLE IF NOT EXISTS environment_cache_policy_history (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK (revision>0 AND revision<=9007199254740991),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot)='object'),
 PRIMARY KEY(app_id,environment_id,revision)
);
-- Current intent wins if a legacy receipt contains the same revision.
INSERT INTO environment_cache_policy_history(account_id,app_id,project_id,environment_id,revision,snapshot)
SELECT account_id,app_id,project_id,environment_id,revision,to_jsonb(p) FROM project_environment_cache_policies p
ON CONFLICT(app_id,environment_id,revision) DO NOTHING;
INSERT INTO environment_cache_policy_history(account_id,app_id,project_id,environment_id,revision,snapshot)
SELECT r.account_id,r.app_id,p.project_id,r.environment_id,(r.response->>'revision')::bigint,r.response
FROM environment_cache_policy_receipts r JOIN project_environment_cache_policies p ON p.account_id=r.account_id AND p.app_id=r.app_id AND p.environment_id=r.environment_id
WHERE r.response->>'app_id'=p.app_id::text AND r.response->>'project_id'=p.project_id::text AND r.response->>'environment_id'=p.environment_id::text
AND (r.response->>'revision')::bigint BETWEEN 1 AND p.revision
ON CONFLICT(app_id,environment_id,revision) DO NOTHING;

-- +goose Down
DROP TABLE environment_cache_policy_history;
