-- filename: 00351_cluster_signing_keys.sql
-- +goose Up
-- +goose StatementBegin

-- Multi-host safety cluster PR-3 (audit F1+F20) / ADR-125.
--
-- Today, schedd's outbound Authorization: Bearer JWT for
-- internal_only apps (ADR-119) is signed by a keypair living on
-- schedd's local disk at FAAS_INTERNAL_SVC_KEY_PATH (or
-- FAAS_INTERNAL_SVC_KEY_SEALED_BLOB). On a multi-host fleet where
-- box A's schedd mints a JWT and box B's gatewayd-internal verifies
-- it, the per-box keypair model means box A's key is not in box B's
-- FAAS_INTERNAL_SVC_PUBKEYS allowlist — the cross-box wake is
-- rejected at the gate with reason="unknown_service". This is
-- audit finding F1+F20 and it is ship-blocking for public
-- release.
--
-- Fix: a single cluster-wide Ed25519 keypair lives in
-- cluster_signing_keys. Every schedd unseals the private key via
-- the host.age identities on its box (multi-box: operator
-- bootstraps a shared host.age onto every box so the unseal loop
-- can open it on every node). Every gatewayd-internal reads the
-- PUBLIC key from the same row and registers the verifier with
-- the cluster key_id.
--
-- The table is a SINGLETON (id=1 CHECK id=1). Rotation creates a
-- new row temporarily or uses UPDATE-in-place with retired_at;
-- see ADR-125 for the rotation protocol. For PR-3 the table holds
-- exactly one active key (retired_at IS NULL).
--
-- Public-key PEM is plaintext (every gatewayd-internal must be able
-- to read it without an unseal dance). The PRIVATE key is sealed
-- with the operator's host.age identity; only schedds and any other
-- future minter-side daemons unseal it. The sealed_blob is opaque
-- age ciphertext — its exact wire shape is owned by pkg/secretbox
-- (see SealBytes + OpenBytesMulti).
--
-- Replay-safe: every CREATE uses IF NOT EXISTS / OR REPLACE so a
-- second goose-up pass (TestNewMigrationsAreReplaySafe) is a no-op.
--
-- Trigger: INSERT / UPDATE / DELETE on cluster_signing_keys fires
-- pg_notify('cluster_signing_keys_changed', TG_TABLE_NAME). Mirrors
-- the compute_node_keys_changed_trg shape (schema.sql:4317-4320)
-- because both surfaces are "a row that affects the wire-side
-- allowlist just changed, refresh your in-memory cache". The
-- listener pattern (cmd/schedd/cluster_key_loader.go +
-- cmd/gatewayd-internal/cluster_key_verifier_loader.go) subscribes
-- to NotifyClusterSigningKeysChanged and re-loads on every delivery.
--
-- Companion ADR: docs/adr/125-fleet-signing-key.md (new).
--
-- Reservation fences: 00352, 00353 — absorbed by PR-4 (cert
-- fingerprint, 00354) and PR-5 (wakeCoord, 00355).

CREATE TABLE IF NOT EXISTS public.cluster_signing_keys (
    id              int          PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    key_id          text         NOT NULL CHECK (key_id ~ '^[a-f0-9]{64}$'),
    public_key_pem  text         NOT NULL CHECK (public_key_pem LIKE '-----BEGIN PUBLIC KEY-----%'),
    sealed_blob     bytea        NOT NULL,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    rotated_at      timestamptz,
    retired_at      timestamptz
);

-- Notify trigger: any change to the row (rotation protocol) fires
-- the cluster_signing_keys_changed channel. The listener pattern
-- in cmd/{schedd,gatewayd-internal}/cluster_*_loader.go is the
-- subscriber; it re-runs LoadClusterSigningKey on every delivery.
-- Mirrors the compute_node_keys_changed_trg shape — one statement
-- trigger, no per-row payload (the listener re-reads the table).
CREATE OR REPLACE FUNCTION public.cluster_signing_keys_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('cluster_signing_keys_changed', TG_TABLE_NAME);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS cluster_signing_keys_changed_trg ON public.cluster_signing_keys;
CREATE TRIGGER cluster_signing_keys_changed_trg
AFTER INSERT OR UPDATE OR DELETE ON public.cluster_signing_keys
FOR EACH STATEMENT EXECUTE FUNCTION public.cluster_signing_keys_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS cluster_signing_keys_changed_trg ON public.cluster_signing_keys;
DROP FUNCTION IF EXISTS public.cluster_signing_keys_notify();
DROP TABLE IF EXISTS public.cluster_signing_keys;

-- +goose StatementEnd
