-- filename: 20261007131321215_runtime_upgrade_external_fence_receipts.sql

-- +goose Up
CREATE TABLE runtime_upgrade_external_fence_authorities (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
 public_key bytea NOT NULL UNIQUE CHECK (octet_length(public_key)=32 AND public_key<>decode(repeat('00',32),'hex')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 revoked_at timestamptz CHECK (revoked_at IS NULL OR (isfinite(revoked_at) AND revoked_at>=created_at))
);
CREATE TABLE runtime_upgrade_external_fence_intents (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
 withdrawal_id uuid NOT NULL REFERENCES runtime_upgrade_public_edge_withdrawals(id),
 authority_id uuid NOT NULL REFERENCES runtime_upgrade_external_fence_authorities(id),
 challenge uuid NOT NULL UNIQUE CHECK (challenge <> '00000000-0000-0000-0000-000000000000'::uuid),
 gateway_revision uuid NOT NULL REFERENCES runtime_upgrade_gateway_rosters(revision),
 public_revision uuid NOT NULL REFERENCES runtime_upgrade_public_edge_rosters(revision),
 machine_id text NOT NULL CHECK (machine_id ~ '^[0-9a-f]{32}$' AND machine_id<>repeat('0',32)),
 boot_id uuid NOT NULL CHECK (boot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 resource_id text NOT NULL CHECK (octet_length(resource_id) BETWEEN 1 AND 256 AND resource_id ~ '^[A-Za-z0-9._:/@-]+$'),
 scope_sha256 text NOT NULL CHECK (scope_sha256 ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at))
);
CREATE INDEX runtime_upgrade_external_fence_intents_withdrawal ON runtime_upgrade_external_fence_intents(withdrawal_id);
CREATE TABLE runtime_upgrade_external_fence_receipts (
 withdrawal_id uuid PRIMARY KEY REFERENCES runtime_upgrade_public_edge_withdrawals(id),
 intent_id uuid NOT NULL UNIQUE REFERENCES runtime_upgrade_external_fence_intents(id),
 receipt_id uuid NOT NULL UNIQUE CHECK (receipt_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 envelope bytea NOT NULL CHECK (octet_length(envelope) BETWEEN 1 AND 16384),
 envelope_sha256 text NOT NULL CHECK (envelope_sha256 ~ '^[0-9a-f]{64}$'),
 enforced_at timestamptz NOT NULL CHECK (isfinite(enforced_at)),
 issued_at timestamptz NOT NULL CHECK (isfinite(issued_at) AND issued_at>=enforced_at),
 observed_at timestamptz NOT NULL CHECK (isfinite(observed_at) AND observed_at>=issued_at)
);
-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_external_fence_authority() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR (TG_OP='INSERT' AND (NEW.revoked_at IS NOT NULL OR NEW.created_at>clock_timestamp())) THEN
  RAISE EXCEPTION 'external authority history cannot be erased or pre-revoked' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL OR NEW.revoked_at>clock_timestamp()
  OR (to_jsonb(NEW)-'revoked_at') IS DISTINCT FROM (to_jsonb(OLD)-'revoked_at')) THEN
  RAISE EXCEPTION 'authority key is immutable and revocation is irreversible' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_external_fence_authority_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_external_fence_authorities FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_external_fence_authority();
-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_external_fence_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE withdrawn runtime_upgrade_public_edge_withdrawals;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'external fence intent is immutable' USING ERRCODE='23514'; END IF;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton AND revision=NEW.gateway_revision FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'external fence internal head changed' USING ERRCODE='23514'; END IF;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton AND revision=NEW.public_revision FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'external fence public head changed' USING ERRCODE='23514'; END IF;
 SELECT * INTO withdrawn FROM runtime_upgrade_public_edge_withdrawals WHERE id=NEW.withdrawal_id FOR UPDATE;
 IF NOT FOUND OR NEW.created_at<withdrawn.created_at OR NEW.created_at>clock_timestamp()
  OR EXISTS(SELECT 1 FROM runtime_upgrade_public_edge_withdrawal_receipts WHERE withdrawal_id=NEW.withdrawal_id)
  OR EXISTS(SELECT 1 FROM runtime_upgrade_external_fence_receipts WHERE withdrawal_id=NEW.withdrawal_id) THEN
  RAISE EXCEPTION 'external fence requires unresolved exact withdrawal' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM runtime_upgrade_external_fence_authorities WHERE id=NEW.authority_id AND revoked_at IS NULL FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'external fence authority unavailable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_external_fence_intent_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_external_fence_intents FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_external_fence_intent();
-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_external_fence_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE reviewed runtime_upgrade_external_fence_intents;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'external fence receipt is immutable' USING ERRCODE='23514'; END IF;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR SHARE;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton FOR SHARE;
 SELECT * INTO reviewed FROM runtime_upgrade_external_fence_intents WHERE id=NEW.intent_id FOR SHARE;
 IF NOT FOUND OR reviewed.withdrawal_id<>NEW.withdrawal_id THEN
  RAISE EXCEPTION 'external receipt intent mismatch' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM runtime_upgrade_public_edge_withdrawals WHERE id=NEW.withdrawal_id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM runtime_upgrade_public_edge_withdrawal_receipts WHERE withdrawal_id=NEW.withdrawal_id)
  OR NOT EXISTS(SELECT 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton AND revision=reviewed.gateway_revision)
  OR NOT EXISTS(SELECT 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton AND revision=reviewed.public_revision) THEN
  RAISE EXCEPTION 'external receipt resolved elsewhere or head changed' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM runtime_upgrade_external_fence_authorities WHERE id=reviewed.authority_id AND revoked_at IS NULL FOR SHARE;
 IF NOT FOUND OR NEW.enforced_at<reviewed.created_at OR NEW.observed_at>clock_timestamp()
  OR clock_timestamp()-NEW.issued_at>interval '60 seconds'
  OR encode(sha256(NEW.envelope),'hex')<>NEW.envelope_sha256 THEN
  RAISE EXCEPTION 'external receipt authority, clock or bytes invalid' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_external_fence_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON runtime_upgrade_external_fence_receipts FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_external_fence_receipt();
-- Both receipt families serialize on the withdrawal; external receipts never
-- invent a local activity version for a terminated host epoch.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_public_edge_withdrawal_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE withdrawn runtime_upgrade_public_edge_withdrawals;
BEGIN
 IF TG_OP <> 'INSERT' THEN RAISE EXCEPTION 'immutable public edge withdrawal receipt' USING ERRCODE='23514'; END IF;
 PERFORM 1 FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR SHARE;
 PERFORM 1 FROM runtime_upgrade_public_edge_roster_head WHERE singleton FOR SHARE;
 SELECT * INTO withdrawn FROM runtime_upgrade_public_edge_withdrawals WHERE id=NEW.withdrawal_id FOR UPDATE;
 IF NOT FOUND OR NEW.observed_at < withdrawn.created_at OR NEW.observed_at > clock_timestamp()
  OR EXISTS(SELECT 1 FROM runtime_upgrade_external_fence_receipts WHERE withdrawal_id=NEW.withdrawal_id)
  OR EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_rosters r JOIN runtime_upgrade_public_edge_roster_head h ON h.revision=r.revision WHERE withdrawn.public_session_id=ANY(r.public_sessions)) THEN
  RAISE EXCEPTION 'withdrawal receipt requires an absent unresolved process and current clock' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
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
  WHERE NOT EXISTS (SELECT 1 FROM runtime_upgrade_public_edge_withdrawal_receipts r WHERE r.withdrawal_id=w.id)
   AND NOT EXISTS (SELECT 1 FROM runtime_upgrade_external_fence_receipts r WHERE r.withdrawal_id=w.id)
  LIMIT 65) pending) > 64 THEN
  RAISE EXCEPTION 'public edge withdrawal capacity exceeded' USING ERRCODE='23514';
 END IF;
 IF NEW.revision IS DISTINCT FROM OLD.revision THEN
  DELETE FROM runtime_upgrade_public_edge_guards;
  DELETE FROM runtime_upgrade_public_edge_activity;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: retain issuer keys, irreversible revocation, challenges and
-- immutable external receipts. Runtime enablement needs native acceptance.
SELECT 1;
