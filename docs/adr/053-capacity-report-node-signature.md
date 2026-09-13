# ADR-053 · `node_signature` on `CapacityReport`

- **Status:** accepted v1.1 (2026-09-13). All code, schema, and tests landed
  on the slice-3 path; this flip retires ADR-025 v1.1's "Tier 1 Phase 2
  (`node_signature` on `CapacityReport`)" pre-requisite.
- **Date:** 2026-07-31
- **Issue:** #95 slice 3 (multi-box rollout, Tier 1 Phase 2)
- **Decision:** Ship the cryptographic trust layer for `CapacityReport`.
  Today's `CapacityReport` (ADR-025 axis 5) is a vmmd→schedd push at ~1 s
  cadence. Slice 1 (ADR-052, PR #445) closed the **transport** gap with
  mTLS — schedd now refuses a peer whose leaf cert doesn't chain, has the
  wrong SAN, or lacks the right EKU. Slice 1 also added handler-layer
  peer binding: schedd inspects `peer.FromContext` and rejects reports
  whose leaf CN doesn't match a registered `compute_node.id`.

  Slice 1 stops at "a vmmd we provisioned". It does NOT pin the report
  contents to a particular vmmd identity — a misconfigured vmmd that has
  a real leaf could still report inflated `used_mb=0` and bias the chooser
  toward itself (the per-node `AdmissionCeilingMB` ceiling in
  `pkg/sched/admission.go:165-168` and the ledger-floor rule in
  `applyLiveCapacityMB` keep the cluster total safe, but the chooser
  bias becomes unreliable). This ADR closes that gap:

  1. **`bytes node_signature = 8`** + **`string node_key_id = 9`** on
     `scheddpb.CapacityReport`. `node_signature` is the 64-byte raw
     (r || s) ECDSA P-256 signature over the canonical payload; `node_key_id`
     is the SHA-256 hex of the leaf's SubjectPublicKeyInfo. The
     `node_key_id` field binds the report to a registered public key
     (one row per key in the new `compute_node_keys` table) so a
     signature alone isn't replayable under a different key rotation.

  2. **Canonical payload.** SHA-256 input is the byte concatenation
     `"faas.capacity.v1" || node_id (UTF-8) || sampled_at_unix_ms
     (big-endian int64) || live_count (big-endian int32) || leased_count
     (big-endian int32) || used_mb (big-endian int32) ||
     ram_headroom_mb (big-endian int32) || vcpu_busy (big-endian int32)`.
     Domain separator first prevents cross-protocol replay (a tier-3
     `cosign` signature on an ext4 layer is structurally different).
     Big-endian fixed-width ints prevent `node_id="00"` vs
     `node_id="" || "0"` ambiguity. P-256 is fixed-curve so r and s
     are each 32 bytes; signature is 64 bytes total.

  3. **Per-node signing key lifecycle.** vmmd loads its signing key from
     `/etc/faas/secrets/vmmd/node.key` (PEM-encoded PKCS#8 ECDSA
     P-256, mode 0400) at startup. If the file is missing or wrong-mode,
     vmmd refuses to start — mirroring the same fail-fast posture as
     `pkg/cosign.NewLocalSigner` and `pkg/secretbox.LoadHostKey`. The
     public half is published to schedd via a new `compute_node_keys`
  table (migration 00076, lifecycle amendment 20260913222058431):
  `(compute_node_id uuid NOT NULL,
     key_id text NOT NULL, public_key_pem text NOT NULL,
     created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY
     key_state text NOT NULL, valid_until timestamptz,
     revoked_at timestamptz, PRIMARY KEY (compute_node_id, key_id))`.
     vmmd registers its own row on
     startup via the same UPSERT that `registerComputeNode` uses
     (idempotent on `(compute_node_id, key_id)`). schedd refreshes
     its in-memory `nodeKeyRegistry` on `pg_notify 'compute_node_changed'`
     (the same trigger migration 00026 fires for the compute_nodes
     table).

  4. **Handler-side verification.** `pkg/scheddgrpc.Server.ReportCapacity`
     calls `sched.VerifyNodeSignature(msg)` *before* the existing
     `table.Replace`. A bad / unknown / stale signature rejects the
     **whole stream** with `codes.Unauthenticated` — not just the bad
     frame — so an attacker can't DoS by sending one valid + 1000
     garbage frames; schedd closes the stream and vmmd reconnects.
     A vmmd whose reports are silently dropped surfaces in the
     `capacity.signature_rejected_total` Prometheus metric and the
     `capacity.signature_rejected` audit row.

  5. **Backward compatibility.** vmmd running against a pre-slice-2
     schedd: the proto field is additive (ADR-016), so old schedd
     ignores `node_signature` + `node_key_id`. vmmd against a slice-2
     schedd: schedd rejects reports with empty `node_signature`
     (codes.Unauthenticated), forcing vmmd to upgrade before the
     cutover. Pre-slice-2 single-box installs with no `compute_nodes`
     row continue to work — the registration path is gated on
     `cfg.ComputeNode.NodeName != ""` (same gate as the publisher).

- **Why:** ADR-025 §6.4 names "CapacityReport trust" as a load-bearing
  failure mode for multi-box. The per-node `AdmissionCeilingMB`
  ceiling in the ledger is the canonical authority on cluster
  capacity, but the chooser uses the report as **bias**, not as
  **authority** — a stale-low or hostile vmmd cannot shrink the live
  accounting below the ledger floor (PR-2 / `applyLiveCapacityMB`'s
  `max(report, ledger.ResidentRAM)`), so the cluster total stays
  safe by construction. The bias, however, becomes unreliable: a
  vmmd that reports `used_mb=0` permanently tilts placement toward
  itself, producing sticky-warm thrash and (in the limit) a
  placement-failure oscillation when one node's bias overwhelms
  another's actual headroom. Signing the report pins its contents
  to the registered identity, so a misconfigured vmmd's reports are
  rejected at the stream boundary and the bias stays clean.

- **Consequences:**
  - **Crypto posture.** vmmd now holds TWO long-lived private keys on
    disk: `/etc/faas/secrets/vmmd/vmmd.key` (mTLS leaf, PR #445) and
    `/etc/faas/secrets/vmmd/node.key` (capacity signing). Both at mode
    0400. The `control_plane_service` ansible role (PR #445) gains
    `stat`-asserts for `node.key` parity. vmmd refuses to start if
    either is missing or wrong-mode.
  - **Worst-case degradation.** A vmmd whose `node.key` was rotated
    without a matching `compute_node_keys` UPSERT keeps its old key
    for one report, then is rejected on the next. vmmd's reconnect
    ladder surfaces this via the `capacity.signature_rejected_total`
    counter; the chooser is unaffected (it falls back to the legacy
    store sum).
  - **Replay window.** `sampled_at_unix_ms` is part of the canonical
    payload, so a replayed report from 30 s ago fails verification.
    schedd does NOT enforce a separate freshness budget on the
    signature itself — the existing 5 s `CapacityFreshness` gate
    already drops stale reports. The two compose.
  - **Cross-node forging.** A vmmd that intercepts another node's
    reports (e.g. via a compromised overlay) cannot re-sign them
    without the victim's `node.key`. The signing key is per-node
    and only on the box that owns it.
  - **Key rotation story.** Ordinary rollouts preserve the on-host keypair.
    An explicit rotation atomically makes the new key current, retains the
    prior key for a 24-hour overlap, and removes older credentials. Schedd
    admits keys only for active nodes, binds each key to its node ID, and
    ignores revoked or expired rows.

## Reference call sites

| Component | File | Change |
|-----------|------|--------|
| proto | `api/proto/onebox/faas/schedd/v1/schedd.proto` | add `bytes node_signature = 8` + `string node_key_id = 9` to `CapacityReport` |
| proto-gen | `api/proto/onebox/faas/schedd/v1/schedd.pb.go` | regenerated by `make proto-gen` |
| migration | `migrations/00076_compute_node_keys.sql` | new table; replay-safe with `IF NOT EXISTS` |
| crypto | `pkg/sched/capacity.go` | new `CapacityReport.CanonicalPayload()` + `SignNodeReport` + `VerifyNodeSignature` helpers |
| registry | `pkg/sched/nodekeys.go` (new) | in-memory registry keyed by key ID with node ownership; loads only active, usable rows from `compute_node_keys` and refreshes on `compute_node_changed` pg_notify |
| handler | `pkg/scheddgrpc/server.go::ReportCapacity` | call `VerifyNodeSignature` before `table.Replace`; reject whole stream on bad signature |
| publisher | `cmd/vmmd/capacity_publisher.go::prodStreamer` + `buildCapacityReport` | load `/etc/faas/secrets/vmmd/node.key` at startup; populate `NodeSignature` + `NodeKeyId` on each report |
| publisher | `cmd/vmmd/main.go::LoadNodeSigningKey` | new helper; mirrors `loadOrGenerateHostIdentity` |
| tests | `pkg/sched/capacity_test.go` | extend with `TestCapacityReportSigned*` (happy path, wrong key, tampered payload, replayed timestamp) |
| tests | `pkg/scheddgrpc/capacity_test.go` (or extend existing test file) | `TestReportCapacityRejectsBadSignature` |
| e2e | `cmd/vmmd/capacity_publisher_e2e_test.go` | round-trip with a real signed report; assert schedd accepts and stores |

## Key material

```
/etc/faas/secrets/vmmd/
├── vmmd.crt                 # mTLS leaf (PR #445), mode 0444
├── vmmd.key                 # mTLS leaf private, mode 0400
└── node.key                 # CapacityReport signing, mode 0400  ← NEW
```

The bootstrap step (`gregalectl pki init`) generates both `vmmd.{crt,key}`
and (per-slice-3) `node.key`. The `control_plane_service` ansible role
gains a per-daemon `stat`-assert for `node.key`.

## Rejected alternatives

- **Sign the report contents INSIDE the TLS record layer (no proto
  change).** Dropping `node_signature` and trusting the mTLS session
  alone. Rejected: the mTLS handshake is between schedd and vmmd-as-
  process, but a misconfigured vmmd whose system clock skews or
  whose instance accounting drifts is still free to send truthful-
  looking-but-wrong numbers. The signature pins the payload bytes
  exactly, so a buggy vmmd is caught too.
- **One signing key cluster-wide (no `compute_node_keys` table).**
  Same key on every box. Rejected: a single key means a single
  point of compromise; one stolen key covers the entire fleet. The
  per-node signing key is the same shape as the per-node mTLS leaf
  (PR #445) — symmetric design.
- **Sign the SHA-256 of the report, not the canonical payload.**
  Drop the domain separator and the fixed-width ints. Rejected:
  a tier-3 `cosign` signature on an ext4 layer (ADR-038) is
  structurally different, but a future protocol that reuses the
  same digest convention would silently collide. The
  `"faas.capacity.v1"` domain separator is the same shape as
  sigstore's Type Hints.
- **Verify per-frame instead of rejecting the whole stream.**
  Catch and drop one bad report, keep the stream open. Rejected:
  the vmmd publisher sends 1/s; an attacker who can plant a bad
  frame costs schedd 1 µs of CPU but produces real bias drift if
  the bad frame happens to land in a window where the ledger hasn't
  observed the real report yet. Closing the stream is loud and
  self-healing via the reconnect ladder.

## Out of scope

- **Rotation audit table.** Trust-set size is exported per node and alerts
  above two; a separate append-only operator audit table remains future work.
- **HSM-backed signing keys.** KMS is ADR-039 territory; not
  relevant here.
