-- filename: 20261017070846822_data_connection_checks.sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS data_connection_checks (
 connection_id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 id uuid NOT NULL UNIQUE,
 connection_revision bigint NOT NULL CHECK (connection_revision>0),
 database_id text NOT NULL DEFAULT '' CHECK (database_id='' OR database_id ~ '^[0-9a-f-]{36}$'),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','running','passed','blocked')),
 result jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(result)='object' AND octet_length(result::text)<=4194304),
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 observed_at timestamptz,
 FOREIGN KEY(account_id,connection_id) REFERENCES data_pipeline_connections(account_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS data_connection_checks_due ON data_connection_checks(created_at,connection_id) WHERE state IN ('pending','running');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS data_connection_checks;
-- +goose StatementEnd
