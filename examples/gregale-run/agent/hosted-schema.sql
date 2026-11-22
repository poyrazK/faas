-- Only the trusted backend and the Report Agent workload can read these tables.
-- Retain requests and their exact grants through the inbox replay window.
CREATE TABLE IF NOT EXISTS report_requests (
  endpoint_id uuid NOT NULL,
  customer_id text NOT NULL,
  request_id uuid NOT NULL,
  thread_id uuid NOT NULL UNIQUE,
  sha256 text NOT NULL CHECK (sha256 ~ '^sha256:[0-9a-f]{64}$'),
  grant_expires_at timestamptz,
  published_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (endpoint_id, customer_id, request_id)
);

CREATE TABLE IF NOT EXISTS report_upload_grants (
  endpoint_id uuid NOT NULL,
  thread_id text NOT NULL,
  call_id text NOT NULL,
  upload_ref uuid NOT NULL,
  grant_token text NOT NULL CHECK (length(grant_token) BETWEEN 1 AND 1536),
  PRIMARY KEY (endpoint_id, thread_id, call_id, upload_ref)
);

-- Regional reports keep their original bounded manifest for exact rerun
-- admission. Each request gets one pipeline and can reserve one child.
CREATE TABLE IF NOT EXISTS report_regional_pipelines (
  thread_id uuid PRIMARY KEY REFERENCES report_requests (thread_id) ON DELETE CASCADE,
  workflow_id text NOT NULL UNIQUE,
  pipeline_id uuid UNIQUE,
  original jsonb NOT NULL CHECK (jsonb_typeof(original) = 'object'),
  parent_thread_id uuid REFERENCES report_regional_pipelines (thread_id),
  repeat_steps jsonb NOT NULL CHECK (jsonb_typeof(repeat_steps) = 'array'),
  rerun_rejected boolean NOT NULL DEFAULT false CHECK (NOT rerun_rejected OR (parent_thread_id IS NOT NULL AND pipeline_id IS NULL)),
  report jsonb CHECK (jsonb_typeof(report) = 'object'),
  CONSTRAINT report_regional_one_child UNIQUE (parent_thread_id)
);

-- Save the exact storage request before attempting the copy. A webhook retry
-- can recover its receipt even after the temporary Run result has expired.
ALTER TABLE report_regional_pipelines ADD COLUMN IF NOT EXISTS delivery_intent jsonb
  CHECK (jsonb_typeof(delivery_intent) = 'object');
ALTER TABLE report_regional_pipelines ADD COLUMN IF NOT EXISTS background_delivery boolean NOT NULL DEFAULT false;
