-- Application-owned rate configuration, token credit and independent history.
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox_rate_limits (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 version integer NOT NULL DEFAULT 1 CHECK(version=1),
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0),
 interval_ms bigint NOT NULL DEFAULT 1000 CHECK(interval_ms BETWEEN 1 AND 3600000),
 burst bigint NOT NULL DEFAULT 1 CHECK(burst BETWEEN 1 AND 10000),
 credit_ms bigint NOT NULL DEFAULT 1000 CHECK(credit_ms BETWEEN 0 AND interval_ms*burst),
 refilled_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(refilled_at)),
 updated_at timestamptz CHECK(updated_at IS NULL OR isfinite(updated_at)),
 last_sequence bigint NOT NULL DEFAULT 0 CHECK(last_sequence>=0),
 PRIMARY KEY(account_id,app_id),
 CHECK((revision=0 AND NOT enabled AND updated_at IS NULL) OR (revision>0 AND updated_at IS NOT NULL))
);
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox_rate_commands (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 command_id uuid NOT NULL,
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 sequence bigint NOT NULL CHECK(sequence>0),
 outcome text NOT NULL CHECK(outcome IN ('configured','disabled','rejected')),
 error_code text NOT NULL CHECK(error_code IN ('','conflict')),
 result_body text NOT NULL CHECK(octet_length(result_body) BETWEEN 1 AND 4096 AND jsonb_typeof(result_body::jsonb)='object'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(recorded_at)),
 PRIMARY KEY(account_id,app_id,command_id),
 UNIQUE(account_id,app_id,sequence),
 CHECK((outcome='rejected')=(error_code='conflict'))
);
