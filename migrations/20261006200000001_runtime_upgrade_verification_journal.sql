-- +goose Up
CREATE TABLE runtime_upgrade_verifications (
 operation_id uuid PRIMARY KEY REFERENCES runtime_upgrade_operations(id) ON DELETE CASCADE,
 gateway_sessions uuid[] NOT NULL CHECK (cardinality(gateway_sessions) BETWEEN 1 AND 64),
 cutover_at timestamptz NOT NULL CHECK (isfinite(cutover_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 deadline_at timestamptz NOT NULL CHECK (isfinite(deadline_at) AND deadline_at = cutover_at + interval '30 minutes'),
 phase text NOT NULL DEFAULT 'pending' CHECK (phase IN ('pending','verified','blocked','expired')),
 reason text NOT NULL DEFAULT '' CHECK (reason ~ '^[a-z_]{0,64}$'),
 last_observation jsonb CHECK (jsonb_typeof(last_observation)='object' AND octet_length(last_observation::text)<=8192),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(next_attempt_at)),
 lease_token uuid CHECK (lease_token<>'00000000-0000-0000-0000-000000000000'::uuid),
 lease_until timestamptz CHECK (isfinite(lease_until)),
 finished_at timestamptz CHECK (isfinite(finished_at)),
 CHECK ((lease_token IS NULL)=(lease_until IS NULL)),
 CHECK ((phase='pending' AND finished_at IS NULL) OR (phase<>'pending' AND finished_at IS NOT NULL AND lease_token IS NULL)),
 CHECK (phase<>'verified' OR (reason='' AND last_observation IS NOT NULL AND last_observation->>'status' IS NOT DISTINCT FROM 'verified'))
);
CREATE INDEX runtime_upgrade_verifications_due ON runtime_upgrade_verifications(next_attempt_at,created_at,operation_id) WHERE phase='pending';

-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_verification() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE canonical uuid[];
BEGIN
 SELECT array_agg(DISTINCT s ORDER BY s) INTO canonical FROM unnest(NEW.gateway_sessions) s;
 IF NEW.gateway_sessions IS DISTINCT FROM canonical OR array_position(NEW.gateway_sessions,NULL::uuid) IS NOT NULL
  OR array_position(NEW.gateway_sessions,'00000000-0000-0000-0000-000000000000'::uuid) IS NOT NULL THEN
  RAISE EXCEPTION 'invalid runtime upgrade verification participants' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NOT EXISTS (SELECT 1 FROM runtime_upgrade_operations o JOIN deployment_runtime_upgrade_cutovers c ON c.deployment_id=o.deployment_id
   WHERE o.id=NEW.operation_id AND o.phase='complete' AND c.cutover_at=NEW.cutover_at AND c.serving_deployment_id=o.serving_deployment_id
   AND c.target_release_id=o.target_release_id AND c.wake_id=o.wake_id AND c.qualification_report_sha256=o.qualification_report_sha256) THEN
   RAISE EXCEPTION 'runtime upgrade verification requires matching activation' USING ERRCODE='23514';
  END IF;
 ELSIF (NEW.operation_id,NEW.gateway_sessions,NEW.cutover_at,NEW.created_at,NEW.deadline_at)
   IS DISTINCT FROM (OLD.operation_id,OLD.gateway_sessions,OLD.cutover_at,OLD.created_at,OLD.deadline_at)
   OR (OLD.phase<>'pending' AND NEW IS DISTINCT FROM OLD) THEN
  RAISE EXCEPTION 'immutable runtime upgrade verification' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_verification_guard BEFORE INSERT OR UPDATE ON runtime_upgrade_verifications FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_verification();

-- +goose Down
-- Forward-only: retain reviewed participants and historical evidence.
SELECT 1;
