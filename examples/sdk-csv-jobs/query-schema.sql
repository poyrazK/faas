-- Backend-owned context, materialized only after verifying the original failed pipeline.
CREATE TABLE sdk_csv_query_imports (
  import_id uuid PRIMARY KEY,
  owner_id text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  source_run_id uuid NOT NULL,
  failure_report text NOT NULL,
  report_sha256 text NOT NULL CHECK (report_sha256 ~ '^[a-f0-9]{64}$')
);
