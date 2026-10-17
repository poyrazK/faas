-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_automation_live_baseline_promotions (
 promotion_id uuid PRIMARY KEY CHECK (promotion_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 app_id uuid NOT NULL CHECK (app_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
 baseline_name text NOT NULL CHECK (octet_length(baseline_name) BETWEEN 1 AND 63 AND baseline_name ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
 revision bigint NOT NULL CHECK (revision > 0),
 suite_id uuid NOT NULL CHECK (suite_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=32768),
 promoted_at timestamptz NOT NULL CHECK (isfinite(promoted_at)),
 UNIQUE(app_id,name,baseline_name,revision),
 FOREIGN KEY(app_id,name) REFERENCES workflow_automation_definitions(app_id,name) ON DELETE CASCADE
);
-- +goose Down
DROP TABLE IF EXISTS workflow_automation_live_baseline_promotions;
