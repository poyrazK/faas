-- filename: 20261008114924492_runtime_upgrade_public_edge_inventory.sql

-- +goose Up
CREATE TABLE runtime_upgrade_public_edge_rosters (
 revision uuid PRIMARY KEY CHECK (revision <> '00000000-0000-0000-0000-000000000000'::uuid),
 gateway_roster_revision uuid NOT NULL REFERENCES runtime_upgrade_gateway_rosters(revision),
 topology_sha256 text NOT NULL CHECK (topology_sha256 ~ '^[0-9a-f]{64}$'),
 slot_ids uuid[] NOT NULL CHECK (cardinality(slot_ids) BETWEEN 1 AND 64 AND array_ndims(slot_ids)=1 AND array_lower(slot_ids,1)=1),
 public_sessions uuid[] NOT NULL CHECK (cardinality(public_sessions)=cardinality(slot_ids) AND array_ndims(public_sessions)=1 AND array_lower(public_sessions,1)=1),
 config_sha256s text[] NOT NULL CHECK (cardinality(config_sha256s)=cardinality(slot_ids) AND array_ndims(config_sha256s)=1 AND array_lower(config_sha256s,1)=1),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at))
);
CREATE TABLE runtime_upgrade_public_edge_roster_head (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 revision uuid REFERENCES runtime_upgrade_public_edge_rosters(revision)
);
INSERT INTO runtime_upgrade_public_edge_roster_head(singleton) VALUES (true);
CREATE TABLE runtime_upgrade_public_edge_guards (
 slot_id uuid PRIMARY KEY CHECK (slot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 public_session_id uuid NOT NULL CHECK (public_session_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 public_roster_revision uuid NOT NULL REFERENCES runtime_upgrade_public_edge_rosters(revision),
 config_sha256 text NOT NULL CHECK (config_sha256 ~ '^[0-9a-f]{64}$'),
 guard_enabled boolean NOT NULL CHECK (guard_enabled),
 observed_at timestamptz NOT NULL CHECK (isfinite(observed_at)),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at=observed_at+interval '1 minute')
);
-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_public_edge_roster() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE canonical uuid[];
BEGIN
 IF TG_OP <> 'INSERT' THEN
  RAISE EXCEPTION 'immutable public edge roster' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton AND revision=NEW.gateway_roster_revision FOR SHARE;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'public edge roster requires current internal roster' USING ERRCODE='23514';
 END IF;
 SELECT array_agg(DISTINCT s ORDER BY s) INTO canonical FROM unnest(NEW.slot_ids) s;
 IF NEW.slot_ids IS DISTINCT FROM canonical
  OR (SELECT count(DISTINCT s) FROM unnest(NEW.public_sessions) s) <> cardinality(NEW.public_sessions)
  OR array_position(NEW.slot_ids,NULL::uuid) IS NOT NULL OR array_position(NEW.public_sessions,NULL::uuid) IS NOT NULL
  OR array_position(NEW.slot_ids,'00000000-0000-0000-0000-000000000000'::uuid) IS NOT NULL
  OR array_position(NEW.public_sessions,'00000000-0000-0000-0000-000000000000'::uuid) IS NOT NULL
  OR EXISTS (SELECT 1 FROM unnest(NEW.config_sha256s) s WHERE s IS NULL OR s !~ '^[0-9a-f]{64}$') THEN
  RAISE EXCEPTION 'invalid public edge roster' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_public_edge_roster_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_public_edge_rosters FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_public_edge_roster();
-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_public_edge_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR SHARE;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton FOR SHARE;
 IF NOT EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_rosters r
  JOIN runtime_upgrade_public_edge_roster_head h ON h.revision=r.revision
  JOIN runtime_upgrade_gateway_roster_head g ON g.revision=r.gateway_roster_revision
  WHERE r.revision=NEW.public_roster_revision
   AND r.public_sessions[array_position(r.slot_ids,NEW.slot_id)]=NEW.public_session_id
   AND r.config_sha256s[array_position(r.slot_ids,NEW.slot_id)]=NEW.config_sha256) THEN
  RAISE EXCEPTION 'public guard fact requires exact current reviewed process and config' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_public_edge_fact_guard BEFORE INSERT OR UPDATE ON runtime_upgrade_public_edge_guards FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_public_edge_fact();
-- +goose Down
-- Forward-only: keep reviewed platform inventory and operational guard facts.
SELECT 1;
