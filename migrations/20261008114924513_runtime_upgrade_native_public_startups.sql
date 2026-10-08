-- filename: 20261008114924513_runtime_upgrade_native_public_startups.sql

-- +goose Up
-- ADR-711: immutable selected startup provenance, never a fencing receipt.
CREATE TABLE IF NOT EXISTS runtime_upgrade_native_public_startups (
 public_session_id uuid PRIMARY KEY CHECK (public_session_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 slot_id uuid NOT NULL CHECK (slot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 gateway_revision uuid NOT NULL REFERENCES runtime_upgrade_gateway_rosters(revision),
 public_revision uuid NOT NULL REFERENCES runtime_upgrade_public_edge_rosters(revision),
 config_sha256 text NOT NULL CHECK (config_sha256 ~ '^[0-9a-f]{64}$'),
 machine_id text NOT NULL CHECK (machine_id ~ '^[0-9a-f]{32}$' AND machine_id<>repeat('0',32)),
 boot_id uuid NOT NULL CHECK (boot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 pid integer NOT NULL CHECK (pid>=2),
 start_ticks text NOT NULL CHECK (start_ticks ~ '^[1-9][0-9]{0,19}$' AND start_ticks::numeric<=18446744073709551615),
 pid_namespace text NOT NULL CHECK (pid_namespace ~ '^pid:\[[1-9][0-9]{0,19}\]$' AND substring(pid_namespace FROM 6 FOR length(pid_namespace)-6)::numeric<=18446744073709551615),
 net_namespace text NOT NULL CHECK (net_namespace ~ '^net:\[[1-9][0-9]{0,19}\]$' AND substring(net_namespace FROM 6 FOR length(net_namespace)-6)::numeric<=18446744073709551615),
 review bytea NOT NULL CHECK (octet_length(review) BETWEEN 1 AND 1048576),
 review_sha256 text NOT NULL CHECK (review_sha256 ~ '^[0-9a-f]{64}$'),
 envelope bytea NOT NULL CHECK (octet_length(envelope) BETWEEN 1 AND 1048576),
 envelope_sha256 text NOT NULL CHECK (envelope_sha256 ~ '^[0-9a-f]{64}$'),
 observed_at timestamptz NOT NULL CHECK (isfinite(observed_at)),
 recorded_at timestamptz NOT NULL CHECK (isfinite(recorded_at) AND recorded_at>=observed_at AND recorded_at-observed_at<=interval '90 seconds')
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_native_public_startup() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE roster runtime_upgrade_public_edge_rosters; body jsonb; selected jsonb; epoch jsonb;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'native startup provenance is immutable' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton AND revision=NEW.gateway_revision FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'native startup internal head changed' USING ERRCODE='23514'; END IF;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton AND revision=NEW.public_revision FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'native startup public head changed' USING ERRCODE='23514'; END IF;
 SELECT * INTO roster FROM runtime_upgrade_public_edge_rosters WHERE revision=NEW.public_revision;
 IF roster.gateway_roster_revision IS DISTINCT FROM NEW.gateway_revision
  OR roster.public_sessions[array_position(roster.slot_ids,NEW.slot_id)] IS DISTINCT FROM NEW.public_session_id
  OR roster.config_sha256s[array_position(roster.slot_ids,NEW.slot_id)] IS DISTINCT FROM NEW.config_sha256
  OR NEW.observed_at<roster.created_at OR NEW.recorded_at>clock_timestamp()
  OR EXISTS(SELECT 1 FROM runtime_upgrade_public_edge_withdrawals WHERE public_session_id=NEW.public_session_id)
  OR NOT EXISTS(SELECT 1 FROM runtime_upgrade_public_edge_guards WHERE slot_id=NEW.slot_id AND public_session_id=NEW.public_session_id
    AND public_roster_revision=NEW.public_revision AND config_sha256=NEW.config_sha256 AND guard_enabled
    AND observed_at<=clock_timestamp() AND expires_at>clock_timestamp()) THEN
  RAISE EXCEPTION 'native startup requires exact current guarded member before withdrawal' USING ERRCODE='23514';
 END IF;
 IF encode(sha256(NEW.review),'hex')<>NEW.review_sha256 OR encode(sha256(NEW.envelope),'hex')<>NEW.envelope_sha256 THEN
  RAISE EXCEPTION 'native startup evidence digest mismatch' USING ERRCODE='23514';
 END IF;
 selected:=convert_from(NEW.review,'UTF8')::jsonb;
 body:=convert_from(NEW.envelope,'UTF8')::jsonb;
 epoch:=body#>'{proof,startup,epoch}';
 IF body->'review' IS DISTINCT FROM selected OR jsonb_array_length(body->'native') IS DISTINCT FROM 2
  OR body#>>'{proof,startup,slot_id}' IS DISTINCT FROM NEW.slot_id::text
  OR body#>>'{proof,startup,session_id}' IS DISTINCT FROM NEW.public_session_id::text
  OR body#>>'{proof,startup,config_sha256}' IS DISTINCT FROM NEW.config_sha256
  OR epoch->>'machine_id' IS DISTINCT FROM NEW.machine_id OR epoch->>'boot_id' IS DISTINCT FROM NEW.boot_id::text
  OR epoch->>'pid' IS DISTINCT FROM NEW.pid::text OR epoch->>'start_ticks' IS DISTINCT FROM NEW.start_ticks
  OR epoch->>'pid_namespace' IS DISTINCT FROM NEW.pid_namespace OR epoch->>'net_namespace' IS DISTINCT FROM NEW.net_namespace THEN
  RAISE EXCEPTION 'native startup evidence identity mismatch' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_native_public_startup_guard ON runtime_upgrade_native_public_startups;
CREATE TRIGGER runtime_upgrade_native_public_startup_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_native_public_startups FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_native_public_startup();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_runtime_upgrade_native_public_startup_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'native startup provenance cannot be truncated' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_native_public_startup_truncate ON runtime_upgrade_native_public_startups;
CREATE TRIGGER runtime_upgrade_native_public_startup_truncate BEFORE TRUNCATE ON runtime_upgrade_native_public_startups FOR EACH STATEMENT EXECUTE FUNCTION forbid_runtime_upgrade_native_public_startup_truncate();
-- +goose Down
-- Forward-only: never erase historical startup provenance.
SELECT 1;
