-- Application-owned PostgreSQL DDL; install explicitly beside business tables.
-- Payloads are sensitive. No SDK helper installs or prunes this table.
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 producer_key text NOT NULL CHECK(octet_length(producer_key) BETWEEN 1 AND 256 AND producer_key ~ '^[!-~]+$'),
 app_slug text NOT NULL CHECK(octet_length(app_slug) BETWEEN 1 AND 256),
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 request_body text CHECK(octet_length(request_body) BETWEEN 1 AND 1048576 AND request_body::jsonb IS NOT NULL),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','unconfirmed','published','blocked')),
 attempts bigint NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 2147483647),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 retry_until timestamptz NOT NULL,
 claim_token bytea CHECK(octet_length(claim_token)=32),
 claim_expires_at timestamptz,
 accepted_at timestamptz,
 last_code text NOT NULL DEFAULT '' CHECK(last_code IN ('','unconfirmed','publication_confirmed','publication_rejected','retry_window_expired')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(account_id,app_id,producer_key),
 CHECK((claim_token IS NULL)=(claim_expires_at IS NULL)),
 CHECK(state IN ('pending','unconfirmed') OR claim_token IS NULL),
 CHECK(state='published' OR request_body IS NOT NULL),
 CHECK((state='published')=(accepted_at IS NOT NULL)),
 CHECK(isfinite(created_at) AND isfinite(updated_at) AND isfinite(next_attempt_at) AND isfinite(retry_until)),
 CHECK(retry_until>created_at AND retry_until<=created_at+interval '168 hours'),
 CHECK(claim_expires_at IS NULL OR isfinite(claim_expires_at))
);
CREATE INDEX IF NOT EXISTS gregale_event_outbox_due
 ON public.gregale_event_outbox(account_id,app_id,next_attempt_at,producer_key)
 WHERE state IN ('pending','unconfirmed');
-- Re-run the opt-in DDL to install this index for an existing outbox.
CREATE INDEX IF NOT EXISTS gregale_event_outbox_unresolved
 ON public.gregale_event_outbox(account_id,app_id,producer_key COLLATE "C")
 WHERE state IN ('blocked','unconfirmed');
-- Health excludes published history and bounds reads by oldest unresolved rows.
CREATE INDEX IF NOT EXISTS gregale_event_outbox_health
 ON public.gregale_event_outbox(account_id,app_id,created_at,producer_key COLLATE "C")
 WHERE state IN ('pending','unconfirmed','blocked');

-- Optional control history: opt in by supplying CommandID on controls.
-- Retain sequence counters across history pruning to preserve pagination order.
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox_control_sequences (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 producer_key text NOT NULL CHECK(octet_length(producer_key) BETWEEN 1 AND 256 AND producer_key ~ '^[!-~]+$'),
 last_sequence bigint NOT NULL CHECK(last_sequence>0),
 PRIMARY KEY(account_id,app_id,producer_key)
);
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox_controls (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 command_id uuid NOT NULL,
 producer_key text NOT NULL CHECK(octet_length(producer_key) BETWEEN 1 AND 256 AND producer_key ~ '^[!-~]+$'),
 action text NOT NULL CHECK(action IN ('reconcile','resume')),
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 sequence bigint NOT NULL CHECK(sequence>0),
 outcome text NOT NULL CHECK(outcome IN ('published','already_published','unavailable','conflict','unverified','resumed','rejected')),
 error_code text NOT NULL CHECK(error_code IN ('','conflict','unconfirmed','busy','not_found')),
 result_body text NOT NULL CHECK(octet_length(result_body) BETWEEN 1 AND 8192 AND jsonb_typeof(result_body::jsonb)='object'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(recorded_at)),
 PRIMARY KEY(account_id,app_id,command_id),
 UNIQUE(account_id,app_id,producer_key,sequence),
 CHECK((action='resume' AND outcome IN ('resumed','rejected')) OR (action='reconcile' AND outcome IN ('published','already_published','unavailable','conflict','unverified'))),
 CHECK(coalesce(result_body::jsonb->>'outcome'=outcome,false))
);
