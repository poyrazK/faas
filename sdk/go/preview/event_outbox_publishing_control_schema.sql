-- Application-owned scope pause and independently retained command history.
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox_publishing_controls (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 paused boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0),
 reason text NOT NULL DEFAULT '' CHECK(octet_length(reason)<=128 AND reason ~ '^[a-z0-9_.-]*$'),
 updated_at timestamptz CHECK(updated_at IS NULL OR isfinite(updated_at)),
 last_sequence bigint NOT NULL DEFAULT 0 CHECK(last_sequence>=0),
 PRIMARY KEY(account_id,app_id),
 CHECK((revision=0 AND NOT paused AND reason='' AND updated_at IS NULL) OR (revision>0 AND updated_at IS NOT NULL))
);
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox_publishing_commands (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 command_id uuid NOT NULL,
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 sequence bigint NOT NULL CHECK(sequence>0),
 outcome text NOT NULL CHECK(outcome IN ('paused','resumed','rejected')),
 error_code text NOT NULL CHECK(error_code IN ('','conflict')),
 result_body text NOT NULL CHECK(octet_length(result_body) BETWEEN 1 AND 4096 AND jsonb_typeof(result_body::jsonb)='object'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(recorded_at)),
 PRIMARY KEY(account_id,app_id,command_id),
 UNIQUE(account_id,app_id,sequence),
 CHECK((outcome='rejected')=(error_code='conflict'))
);
