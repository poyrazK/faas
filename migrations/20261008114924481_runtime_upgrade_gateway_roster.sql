-- filename: 20261008114924481_runtime_upgrade_gateway_roster.sql

-- +goose Up
CREATE TABLE runtime_upgrade_gateway_rosters (
 revision uuid PRIMARY KEY CHECK (revision<>'00000000-0000-0000-0000-000000000000'::uuid),
 slot_ids uuid[] NOT NULL CHECK (cardinality(slot_ids) BETWEEN 1 AND 64 AND array_ndims(slot_ids)=1 AND array_lower(slot_ids,1)=1),
 gateway_sessions uuid[] NOT NULL CHECK (cardinality(gateway_sessions)=cardinality(slot_ids) AND array_ndims(gateway_sessions)=1 AND array_lower(gateway_sessions,1)=1),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at))
);
CREATE TABLE runtime_upgrade_gateway_roster_head (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 revision uuid REFERENCES runtime_upgrade_gateway_rosters(revision)
);
INSERT INTO runtime_upgrade_gateway_roster_head(singleton) VALUES (true);
CREATE TABLE runtime_upgrade_gateway_heartbeats (
 slot_id uuid PRIMARY KEY CHECK (slot_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 gateway_session_id uuid NOT NULL CHECK (gateway_session_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 roster_revision uuid NOT NULL REFERENCES runtime_upgrade_gateway_rosters(revision),
 seen_at timestamptz NOT NULL CHECK (isfinite(seen_at)),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at=seen_at+interval '1 minute')
);
ALTER TABLE runtime_upgrade_verifications ADD COLUMN gateway_roster_revision uuid REFERENCES runtime_upgrade_gateway_rosters(revision);

-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_gateway_roster() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE canonical uuid[]; unique_sessions integer;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'immutable runtime upgrade gateway roster' USING ERRCODE='23514';
 END IF;
 SELECT array_agg(DISTINCT s ORDER BY s) INTO canonical FROM unnest(NEW.slot_ids) s;
 SELECT count(DISTINCT s) INTO unique_sessions FROM unnest(NEW.gateway_sessions) s;
 IF NEW.slot_ids IS DISTINCT FROM canonical OR unique_sessions<>cardinality(NEW.gateway_sessions)
  OR array_position(NEW.slot_ids,NULL::uuid) IS NOT NULL OR array_position(NEW.gateway_sessions,NULL::uuid) IS NOT NULL
  OR array_position(NEW.slot_ids,'00000000-0000-0000-0000-000000000000'::uuid) IS NOT NULL
  OR array_position(NEW.gateway_sessions,'00000000-0000-0000-0000-000000000000'::uuid) IS NOT NULL THEN
  RAISE EXCEPTION 'invalid runtime upgrade gateway roster' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_gateway_roster_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_gateway_rosters FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_gateway_roster();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_verification() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE canonical uuid[];
BEGIN
 SELECT array_agg(DISTINCT s ORDER BY s) INTO canonical FROM unnest(NEW.gateway_sessions) s;
 IF NEW.gateway_sessions IS DISTINCT FROM canonical OR array_position(NEW.gateway_sessions,NULL::uuid) IS NOT NULL
  OR array_position(NEW.gateway_sessions,'00000000-0000-0000-0000-000000000000'::uuid) IS NOT NULL THEN
  RAISE EXCEPTION 'invalid runtime upgrade verification participants' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' THEN
  PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR SHARE;
  IF NEW.gateway_roster_revision IS NULL OR NOT EXISTS (
   SELECT 1 FROM runtime_upgrade_gateway_rosters r JOIN runtime_upgrade_gateway_roster_head h ON h.revision=r.revision
   WHERE r.revision=NEW.gateway_roster_revision AND NEW.gateway_sessions=(SELECT array_agg(s ORDER BY s) FROM unnest(r.gateway_sessions) s)) THEN
   RAISE EXCEPTION 'runtime upgrade verification requires full current gateway roster' USING ERRCODE='23514';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM runtime_upgrade_operations o JOIN deployment_runtime_upgrade_cutovers c ON c.deployment_id=o.deployment_id
   WHERE o.id=NEW.operation_id AND o.phase='complete' AND c.cutover_at=NEW.cutover_at AND c.serving_deployment_id=o.serving_deployment_id
   AND c.target_release_id=o.target_release_id AND c.wake_id=o.wake_id AND c.qualification_report_sha256=o.qualification_report_sha256) THEN
   RAISE EXCEPTION 'runtime upgrade verification requires matching activation' USING ERRCODE='23514';
  END IF;
 ELSIF (NEW.operation_id,NEW.gateway_sessions,NEW.gateway_roster_revision,NEW.cutover_at,NEW.created_at,NEW.deadline_at)
   IS DISTINCT FROM (OLD.operation_id,OLD.gateway_sessions,OLD.gateway_roster_revision,OLD.cutover_at,OLD.created_at,OLD.deadline_at)
   OR (OLD.phase<>'pending' AND NEW IS DISTINCT FROM OLD) THEN
  RAISE EXCEPTION 'immutable runtime upgrade verification' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: retain reviewed membership and historical verification.
SELECT 1;
