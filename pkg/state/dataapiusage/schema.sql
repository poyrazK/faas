-- Compile-time projection of the tables the Data API usage queries read.
-- Runtime schema is owned by migrations 20261019131100664 and
-- 20261019172254731 (and, for apps, earlier migrations). This file never
-- executes against a database.
CREATE TABLE apps (
 id uuid NOT NULL,
 account_id uuid NOT NULL,
 deleted_at timestamptz
);
CREATE TABLE data_api_usage_hourly (
 app_id uuid NOT NULL,
 hour timestamptz NOT NULL,
 user_requests bigint NOT NULL DEFAULT 0,
 anonymous_requests bigint NOT NULL DEFAULT 0,
 server_requests bigint NOT NULL DEFAULT 0,
 rejected_requests bigint NOT NULL DEFAULT 0,
 error_requests bigint NOT NULL DEFAULT 0,
 capped_requests bigint NOT NULL DEFAULT 0,
 rows_returned bigint NOT NULL DEFAULT 0,
 response_bytes bigint NOT NULL DEFAULT 0,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (app_id, hour)
);
CREATE TABLE data_api_usage_reports (
 app_id uuid NOT NULL,
 report_id text NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (app_id, report_id)
);
CREATE TABLE data_api_access_configs (
 app_id uuid PRIMARY KEY,
 config jsonb NOT NULL,
 reported_at timestamptz NOT NULL DEFAULT now()
);
