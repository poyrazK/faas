# ADR-125 · Fleet-wide schedd signing key (PG-distributed)

- **Status:** accepted (2026-08-22)
- **Issue:** multi-host safety cluster PR-3 / audit F1+F20
- **Decision:** Replace per-host Ed25519 keypair (FAAS_INTERNAL_SVC_KEY_PATH / FAAS_INTERNAL_SVC_PUBKEYS) with a single cluster-wide keypair stored in `public.cluster_signing_keys` and distributed via PG + `pg_notify('cluster_signing_keys_changed')`. Every schedd in the fleet mints with the same kid; every gatewayd-internal verifies against the same public key.

## Context

The ADR-119 outbound Authorization: Bearer JWT that schedd attaches to internal_only requests is signed by an Ed25519 keypair loaded at boot from FAAS_INTERNAL_SVC_KEY_PATH (plaintext PEM) or FAAS_INTERNAL_SVC_KEY_SEALED_BLOB (age-encrypted PEM). The corresponding public key is provisioned via FAAS_INTERNAL_SVC_PUBKEYS (JSON `{svcName: PEM}`) on every gatewayd-internal box.

On a single-host install this works because there is exactly one schedd and one gatewayd-internal. On a multi-host fleet where schedd on box A mints a JWT for an app routed to gatewayd-internal on box B, box B's FAAS_INTERNAL_SVC_PUBKEYS does NOT contain box A's key — the verify step fails with reason="unknown_service". The cross-box internal_only wake is rejected at the gate even when the underlying wakeCoord, schedd-router, and transport are otherwise healthy.

This is audit finding F1+F20. It is ship-blocking for public release because the spec §4.3 "internal_only mode" requires cross-box sign/verify to work transparently.

## Decision

Three components:

1. **`public.cluster_signing_keys`** (migration 00351) — a singleton table (id=1, CHECK id=1) holding one active Ed25519 keypair per cluster. Columns: `key_id text` (64-hex matching kid shape), `public_key_pem text` (PEM-encoded PKIX Ed25519 public key), `sealed_blob bytea` (private key sealed with the operator's host.age identity), `created_at`, `rotated_at`, `retired_at`. Insert trigger fires `pg_notify('cluster_signing_keys_changed', TG_TABLE_NAME)` on any INSERT/UPDATE/DELETE.

2. **`pgstate.PgStore` LoadClusterSigningKey / InsertClusterSigningKey / DeleteClusterSigningKey** (pkg/state/pgstore_cluster_keys.go) — the canonical Store-layer surface. Load returns `ErrNotFound` on an empty table (the operator-migration window); Insert uses ON CONFLICT DO UPDATE for rotation protocol.

3. **Schedd minter (cmd/schedd/internal_svc_minter.go + cmd/schedd/cluster_key_loader.go)** — new fallback chain:

   ```
   cluster_signing_keys row (PR-3 / ADR-125)
     └─ on ErrClusterKeyUnavailable → per-host FAAS_INTERNAL_SVC_KEY_PATH / FAAS_INTERNAL_SVC_KEY_SEALED_BLOB
          └─ on missing file → generate fresh + WARN (legacy behaviour)
   ```

   The cluster row is unsealed via `secretbox.OpenBytesMulti(identities, sealed_blob)` against the host.age chain on this box (multi-box: operator bootstraps the sealing identity onto every box). The unsealed PEM is parsed as PKCS#8 Ed25519; the derived kid is asserted equal to the row's key_id column (the row-internal-consistency guard).

   The minter closure is backed by an `atomicMinter` (atomic.Pointer swap). A long-lived subscriber goroutine (`SubscribeClusterKeyChanges`) listens to `cluster_signing_keys_changed` and calls `atomicMinter.Rotate(priv, kid)` on every delivery. Rotation lands within one Postgres NOTIFY round-trip (~5 ms end-to-end) without dropping in-flight synth requests.

4. **Gatewayd-internal verifier (cmd/gatewayd-internal/cluster_key_verifier_loader.go + internal_svc_verifier.go)** — symmetric mirror. New `loadClusterInternalSvcVerifier(ctx, store)` reads the row, builds a one-key allowlist `{ "schedd": PublicKeyPEM }` via the existing bridge (`newInternalSvcVerifierFromPEMs`), and wraps it in a `rotatingVerifier` (atomic.Pointer-swap wrapper satisfying `gateway.InternalSvcVerifier`). The wrapper preserves the gate's fail-closed semantics during the rotation window (nil current → "verifier wired but no keys"). Long-lived subscriber goroutine (`SubscribeClusterVerifierChanges`) swaps the verifier on every delivery.

   Fallback chain (in order): cluster_signing_keys → FAAS_INTERNAL_SVC_PUBKEYS env → nil (gate 500s).

## Why PG-distributed

- **Single source of truth across the fleet.** A row in `cluster_signing_keys` is the only thing schedd on box A and gatewayd-internal on box B both consult. Disk-based secrets cannot guarantee consistency across boxes; environment variables set at ansible-apply time are not synchronized with rotation events.
- **Rotation atomicity.** A single transaction updates the row + stamps `retired_at`; the cluster-wide effect lands within one Postgres COMMIT. The trigger fires; every minter/verifier picks up the change within ~5 ms.
- **pg_notify is already the project's broadcast primitive.** Every daemon (schedd, gatewayd-internal, imaged, builderd, apid) subscribes to one or more channels via `db.SubscribeWithReconnect`. Adding a new channel is the same shape; no new infrastructure.
- **Encrypted at rest.** The private key never lands on disk in plaintext; it lives as age ciphertext in PG. The unseal envelope is owned by pkg/secretbox; same primitive the per-host path uses today.

## Why not per-host key + PG-cached public key

A simpler approach: each schedd keeps its own keypair; gatewayd-internal caches the per-host public keys in PG keyed by node_name. Cross-box auth requires looking up the minter's node first.

The problems:
- The mint side has to know its own node_name and stamp it on every JWT.
- The verify side has to look up the right key by node_name from the JWT's `iss` claim, which the trust boundary cannot trust (a compromised box could mint a JWT claiming to be a different node).
- Rotation requires updating N rows across N boxes; not atomic.
- The "single cluster key" semantic doesn't match the JWT contract: a kid is supposed to identify a key, not a (key, node) tuple. A token's verifier picks one key by kid; if two boxes share a kid, the verifier can't distinguish them.

The PG-distributed single-key model matches the JWT contract cleanly: kid identifies the key, the verifier picks the kid, the key is the cluster key.

## Why not just use FAAS_INTERNAL_SVC_PUBKEYS across boxes

The env-path is static (set at ansible-apply time). It cannot express rotation: a new key needs every gatewayd-internal's env to be re-rendered and the daemon restarted. The PG path expresses rotation as a row update + pg_notify; no restart.

## Why a sealed blob in PG, not a cleartext blob

CLAUDE.md §11 + §17 G2 (round-3 peer-review): secrets at rest are sealed via host.age. The operator's `hostage-gen cluster-init` (out of scope for PR-3; the in-tree helper is `pgstate.PgStore.InsertClusterSigningKey`) seals the private key with the operator's host.age identity and inserts the ciphertext. Schedds unseal via the host.age identities on their own boxes; the unseal loop is the same `secretbox.OpenBytesMulti` every other sealed secret uses.

## Why one row, not "current + previous" for rotation overlap

The rotation-overlap pattern (verifier accepts both the retiring kid and the new kid during a grace window) is the right long-term design. PR-3 ships the table shape forward-compatible — `retired_at` exists, and `LoadClusterSigningKey` returns a single row, but the gatewayd-internal verifier consumes a one-key allowlist. The follower amendment to ADR-125 (a follow-on PR cluster member) will:
- Add a "list" loader returning all non-retired + recently-retired (retired_at > now() - rotation_window) rows.
- Extend the gatewayd-internal verifier allowlist to `map[svcName]map[kid]ed25519.PublicKey` so a token's kid is looked up directly instead of collapsed onto the per-svc map.
- Extend schedd's minter subscriber to refresh both priv+kid on rotation, keeping the previous public-key table entry alive for the overlap window.

PR-3 ships the storage + the loader + the rotation subscriber. The verifier-side overlap is the only piece deferred, and the deferred piece does not block F1+F20: a fresh install's single-key state works end-to-end today.

## Backwards compat

- Empty `cluster_signing_keys` table → schedd falls back to per-host FAAS_INTERNAL_SVC_KEY_PATH / FAAS_INTERNAL_SVC_KEY_SEALED_BLOB; gatewayd-internal falls back to FAAS_INTERNAL_SVC_PUBKEYS. Both paths are unchanged from ADR-119.
- Operators who haven't run `hostage-gen cluster-init` see no behaviour change. Operators who have see the new cluster-key path.
- The migration is replay-safe (CREATE TABLE IF NOT EXISTS, CREATE OR REPLACE FUNCTION, DROP TRIGGER IF EXISTS).

## Consequences

- **Single critical section per cluster for internal-svc auth.** Every schedd unseals the same sealed blob; every gatewayd-internal reads the same public key. The cross-box verify path now works.
- **Rotation is automatic and atomic.** The operator's `hostage-gen cluster-rotate` (out of scope for PR-3) updates the row; every daemon picks up the new key within ~5 ms. No restart required. In-flight JWTs minted with the retiring kid remain valid for the rotation overlap window once the follower amendment lands.
- **Operator-migration window.** An existing single-box install with a populated FAAS_INTERNAL_SVC_KEY_PATH keeps working. The first time `hostage-gen cluster-init` runs (or the operator runs the manual SQL + sealed-blob flow), the box starts using the cluster path on next boot.
- **Failure modes:** empty cluster_signing_keys + empty per-host fallback = loud error at boot (legacy behaviour). Empty cluster_signing_keys + per-host fallback present = legacy path used (PR-3 silent fallback). Cluster row unseal fails on this box = same as empty (PR-3 silent fallback with WARN log). Postgres unreachable at boot = mint fails to load, gate 500s loudly (existing loud-misconfig posture).

## Rejected alternatives

- **Per-box keys + PG-cached public keys.** See "Why not per-host key" above. Requires a node-aware mint/verify path; the JWT contract doesn't support a (key, node) tuple cleanly.
- **JWKS endpoint (gatewayd-public) for cross-box public-key distribution.** Adds an HTTP fetch to every verify (cold-path latency) and a single endpoint to maintain. PG is the project's existing source-of-truth for fleet-wide state; a new endpoint is more surface for no clear win.
- **Out-of-band key-distribution (HashiCorp Vault, AWS KMS, etc.).** Introduces a new infrastructure dependency for a problem PG already solves. The financial model does not budget for a Vault operator; this is a non-starter for the EX44 baseline.
- **Per-svc keypair (schedd / meterd / imaged / builderd).** Each daemon gets its own row in cluster_signing_keys. The table is currently a singleton (id=1); extending to per-svc is a one-column migration + per-svc loader. PR-3 ships the singleton shape; per-svc is a clean follow-on.
- **Cert-based auth (mTLS across boxes).** ADR-119 already established JWT as the contract. Switching to mTLS would require renegotiating every existing internal-svc contract; the cost outweighs the benefit.

## Follow-on

- **Rotation-overlap loader** (follow-on PR cluster member): extend `LoadClusterSigningKey` to return all non-retired + recently-retired rows; extend gatewayd-internal verifier allowlist to per-svc-per-kid map. The verifier-side overlap is the only piece PR-3 deliberately defers.
- **`hostage-gen cluster-init`** (out of scope for PR-3; flagged here as the operator-bootstrap CLI): generate the cluster keypair, seal with host.age, insert the row, print the public_key_pem for operator-side reference.
- **Per-svc cluster_signing_keys rows** (PR-3+1): drop the CHECK (id = 1) singleton constraint, add a `svc_name text` column + UNIQUE(svc_name), and migrate schedd's issuer off "schedd" to per-svc mapping. The PR-3 singleton shape is forward-compatible (no follow-on migration required to add svc_name — it's an additive change).
