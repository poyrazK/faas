-- filename: 20261008114924497_runtime_upgrade_public_edge_activity.sql

-- +goose Up
CREATE TABLE runtime_upgrade_public_edge_activity (
 slot_id uuid PRIMARY KEY CHECK (slot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 public_session_id uuid NOT NULL CHECK (public_session_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 public_roster_revision uuid NOT NULL REFERENCES runtime_upgrade_public_edge_rosters(revision),
 config_sha256 text NOT NULL CHECK (config_sha256 ~ '^[0-9a-f]{64}$'),
 guard_enabled boolean NOT NULL CHECK (guard_enabled),
 activity_version bigint NOT NULL CHECK (activity_version > 0),
 coverage_known boolean NOT NULL,
 pending_forwards integer NOT NULL CHECK (pending_forwards BETWEEN 0 AND 65536),
 current_forwards integer NOT NULL CHECK (current_forwards BETWEEN 0 AND 65536),
 previous_forwards integer NOT NULL CHECK (previous_forwards BETWEEN 0 AND 65536),
 observed_at timestamptz NOT NULL CHECK (isfinite(observed_at)),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at=observed_at+interval '1 minute'),
 CHECK (pending_forwards+current_forwards+previous_forwards <= 65536)
);
CREATE TRIGGER runtime_upgrade_public_edge_activity_membership_guard BEFORE INSERT OR UPDATE ON runtime_upgrade_public_edge_activity FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_public_edge_fact();
-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_public_edge_activity_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.public_session_id=NEW.public_session_id AND OLD.public_roster_revision=NEW.public_roster_revision AND (
  NEW.activity_version < OLD.activity_version
  OR (NOT OLD.coverage_known AND NEW.coverage_known)
  OR (NEW.activity_version=OLD.activity_version AND
   (NEW.coverage_known,NEW.pending_forwards,NEW.current_forwards,NEW.previous_forwards)
   IS DISTINCT FROM (OLD.coverage_known,OLD.pending_forwards,OLD.current_forwards,OLD.previous_forwards))) THEN
  RAISE EXCEPTION 'public ingress activity cannot rewind or restore unknown coverage' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_public_edge_activity_version_guard BEFORE UPDATE ON runtime_upgrade_public_edge_activity FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_public_edge_activity_version();
-- +goose Down
-- Forward-only: retain operational generation observations.
SELECT 1;
