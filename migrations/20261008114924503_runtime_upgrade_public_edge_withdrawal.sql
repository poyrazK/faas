-- filename: 20261008114924503_runtime_upgrade_public_edge_withdrawal.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS runtime_upgrade_public_edge_withdrawals (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
 slot_id uuid NOT NULL CHECK (slot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 public_session_id uuid NOT NULL UNIQUE CHECK (public_session_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 config_sha256 text NOT NULL CHECK (config_sha256 ~ '^[0-9a-f]{64}$'),
 roster_revision uuid NOT NULL REFERENCES runtime_upgrade_public_edge_rosters(revision),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at))
);
CREATE TABLE IF NOT EXISTS runtime_upgrade_public_edge_withdrawal_receipts (
 withdrawal_id uuid PRIMARY KEY REFERENCES runtime_upgrade_public_edge_withdrawals(id),
 fence_id uuid NOT NULL CHECK (fence_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 activity_version bigint NOT NULL CHECK (activity_version > 0),
 admission_closed boolean NOT NULL CHECK (admission_closed),
 coverage_known boolean NOT NULL CHECK (coverage_known),
 active_forwards integer NOT NULL CHECK (active_forwards=0),
 observed_at timestamptz NOT NULL CHECK (isfinite(observed_at))
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_public_edge_withdrawal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP <> 'INSERT' THEN
  RAISE EXCEPTION 'immutable public edge withdrawal' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR SHARE;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton FOR SHARE;
 IF EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_rosters r JOIN runtime_upgrade_public_edge_roster_head h ON h.revision=r.revision WHERE NEW.public_session_id=ANY(r.public_sessions))
  OR NOT EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_rosters r WHERE r.revision=NEW.roster_revision
   AND r.public_sessions[array_position(r.slot_ids,NEW.slot_id)]=NEW.public_session_id
   AND r.config_sha256s[array_position(r.slot_ids,NEW.slot_id)]=NEW.config_sha256) THEN
  RAISE EXCEPTION 'withdrawal requires exact previously reviewed absent process' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_public_edge_withdrawal_guard ON runtime_upgrade_public_edge_withdrawals;
CREATE TRIGGER runtime_upgrade_public_edge_withdrawal_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_public_edge_withdrawals FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_public_edge_withdrawal();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_public_edge_withdrawal_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE withdrawn runtime_upgrade_public_edge_withdrawals;
BEGIN
 IF TG_OP <> 'INSERT' THEN
  RAISE EXCEPTION 'immutable public edge withdrawal receipt' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR SHARE;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton FOR SHARE;
 SELECT * INTO withdrawn FROM runtime_upgrade_public_edge_withdrawals WHERE id=NEW.withdrawal_id FOR SHARE;
 IF NOT FOUND OR NEW.observed_at < withdrawn.created_at OR NEW.observed_at > clock_timestamp()
  OR EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_rosters r JOIN runtime_upgrade_public_edge_roster_head h ON h.revision=r.revision WHERE withdrawn.public_session_id=ANY(r.public_sessions)) THEN
  RAISE EXCEPTION 'withdrawal receipt requires an absent reviewed process and current clock' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_public_edge_withdrawal_receipt_guard ON runtime_upgrade_public_edge_withdrawal_receipts;
CREATE TRIGGER runtime_upgrade_public_edge_withdrawal_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_public_edge_withdrawal_receipts FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_public_edge_withdrawal_receipt();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_public_edge_head_withdrawals() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_roster runtime_upgrade_public_edge_rosters;
BEGIN
 IF TG_OP='DELETE' OR NEW.revision IS NULL THEN
  RAISE EXCEPTION 'public edge head cannot erase withdrawal history' USING ERRCODE='23514';
 END IF;
 SELECT * INTO next_roster FROM runtime_upgrade_public_edge_rosters WHERE revision=NEW.revision;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton AND revision=next_roster.gateway_roster_revision FOR SHARE;
 IF NOT FOUND OR EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_withdrawals w WHERE w.public_session_id=ANY(next_roster.public_sessions)) THEN
  RAISE EXCEPTION 'public review cannot resurrect a withdrawn process or bind an old internal review' USING ERRCODE='23514';
 END IF;
 IF EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_rosters prior, generate_subscripts(prior.public_sessions,1) i
  WHERE prior.revision=OLD.revision AND prior.public_sessions[i]=ANY(next_roster.public_sessions)
   AND (prior.slot_ids[i],prior.config_sha256s[i]) IS DISTINCT FROM
    (next_roster.slot_ids[array_position(next_roster.public_sessions,prior.public_sessions[i])],next_roster.config_sha256s[array_position(next_roster.public_sessions,prior.public_sessions[i])])) THEN
  RAISE EXCEPTION 'startup session cannot change public slot or configuration' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_public_edge_head_withdrawal_guard ON runtime_upgrade_public_edge_roster_head;
CREATE TRIGGER runtime_upgrade_public_edge_head_withdrawal_guard BEFORE UPDATE OR DELETE ON runtime_upgrade_public_edge_roster_head FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_public_edge_head_withdrawals();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_runtime_upgrade_public_edge_withdrawals() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE added bigint;
BEGIN
 INSERT INTO runtime_upgrade_public_edge_withdrawals(id,slot_id,public_session_id,config_sha256,roster_revision)
 SELECT gen_random_uuid(),prior.slot_ids[i],prior.public_sessions[i],prior.config_sha256s[i],prior.revision
 FROM runtime_upgrade_public_edge_rosters prior, generate_subscripts(prior.public_sessions,1) i
 WHERE prior.revision=OLD.revision AND NOT EXISTS
  (SELECT 1 FROM runtime_upgrade_public_edge_rosters next WHERE next.revision=NEW.revision AND prior.public_sessions[i]=ANY(next.public_sessions))
 ON CONFLICT (public_session_id) DO NOTHING;
 GET DIAGNOSTICS added=ROW_COUNT;
 IF added > 0 AND (SELECT count(*) FROM (SELECT 1 FROM runtime_upgrade_public_edge_withdrawals w
  WHERE NOT EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_withdrawal_receipts r WHERE r.withdrawal_id=w.id) LIMIT 65) pending) > 64 THEN
  RAISE EXCEPTION 'public edge withdrawal capacity exceeded' USING ERRCODE='23514';
 END IF;
 -- Direct head publication must also invalidate current facts. Otherwise a
 -- topology-only revision round trip could borrow an older zero-activity fact.
 IF NEW.revision IS DISTINCT FROM OLD.revision THEN
  DELETE FROM runtime_upgrade_public_edge_guards;
  DELETE FROM runtime_upgrade_public_edge_activity;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS runtime_upgrade_public_edge_withdrawal_capture ON runtime_upgrade_public_edge_roster_head;
CREATE TRIGGER runtime_upgrade_public_edge_withdrawal_capture AFTER UPDATE ON runtime_upgrade_public_edge_roster_head FOR EACH ROW EXECUTE FUNCTION capture_runtime_upgrade_public_edge_withdrawals();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION seed_runtime_upgrade_public_edge_withdrawals() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR SHARE;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton FOR SHARE;
 INSERT INTO runtime_upgrade_public_edge_withdrawals(id,slot_id,public_session_id,config_sha256,roster_revision)
 SELECT gen_random_uuid(),slot_id,public_session_id,config_sha256,revision FROM (
  SELECT DISTINCT ON (r.public_sessions[i]) r.slot_ids[i] AS slot_id,r.public_sessions[i] AS public_session_id,r.config_sha256s[i] AS config_sha256,r.revision
  FROM runtime_upgrade_public_edge_rosters r, generate_subscripts(r.public_sessions,1) i
  WHERE NOT EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_rosters live JOIN runtime_upgrade_public_edge_roster_head h ON h.revision=live.revision WHERE r.public_sessions[i]=ANY(live.public_sessions))
  ORDER BY r.public_sessions[i],r.created_at DESC,r.revision DESC
 ) historical ON CONFLICT (public_session_id) DO NOTHING;
END $$;
-- +goose StatementEnd
-- Preserve ALL legacy history even if it exceeds the new unresolved bound.
SELECT seed_runtime_upgrade_public_edge_withdrawals();

-- +goose Down
-- Forward-only: retain permanent process withdrawal intent and sealed evidence.
SELECT 1;
