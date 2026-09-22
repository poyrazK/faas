# ADR-058 · cosign signature enforcement at deploy time

- **Status:** proposed
- **Date:** 2026-08-01
- **Issue:** #472 (SEC: enforce cosign signature at deploy time)
- **Supersedes:** — (extends ADR-038 build-side signing primitive)
- **Decision:** Close the gap between PR #371's build-side `gregale
  sign-keys` and a regulated-workload deploy-time gate by adding a
  per-app `require_signed` flag plus a per-app trusted-publisher list.
  Default off. Mirrors AWS Lambda's Code Signing for Lambda (2020).

## Why

PR #371 (merged) shipped `gregalectl sign-keys init|rotate|status` and
`pkg/cosign`, so operators can generate and rotate a cosign keypair
on the box. ADR-038's build-side wiring (used by schedd at cold-boot)
verifies ext4 layer signatures against `/etc/faas/secrets/sign-pub.pem`.

What was still missing was **deploy-time enforcement**: today
`POST /v1/apps/{slug}/deployments` accepts any OCI image regardless
of signature. For regulated workloads (healthcare / fintech / SOC 2 /
ISO 27001 / PCI-DSS), "trust the developer's machine" is not an
acceptable posture. Lambda's `Code Signing for Lambda` is the
reference design: a per-function trusted-publisher list plus an
`Enforce code signing` toggle.

This ADR commits to:

1. **A per-app `require_signed` flag** on the apps row
   (`ALTER TABLE apps ADD COLUMN IF NOT EXISTS require_signed
   boolean NOT NULL DEFAULT false`). Default false. When true,
   OCI image deploys to this app must carry a valid cosign
   signature from a publisher in the per-app trusted-publisher list.

2. **A per-app `app_trusted_signers` table** mirroring Lambda's
   `CodeSigningConfig`:

   ```sql
   CREATE TABLE IF NOT EXISTS app_trusted_signers (
       app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
       signer_name text NOT NULL,
       cosign_public_key bytes NOT NULL,
       added_at timestamptz NOT NULL DEFAULT now(),
       added_by_user_id uuid NOT NULL REFERENCES users(id),
       PRIMARY KEY (app_id, signer_name),
       CONSTRAINT app_trusted_signers_name_shape CHECK
           (signer_name ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
       CONSTRAINT app_trusted_signers_pem_shape CHECK
           (octet_length(cosign_public_key) BETWEEN 64 AND 1024)
   );
   ```

3. **Three admin+MFA endpoints** mounted on apid, restricted to the
   `ScopesAdminOnly` scope:
   - `PATCH /v1/apps/{slug}/security` — toggle `require_signed`.
     Pointer-shaped field so the wire can distinguish "don't touch"
     (nil) from "explicit true/false" (issue #471 streaming flag
     precedent).
   - `GET /v1/apps/{slug}/trusted_signers` — list publishers.
   - `PUT /v1/apps/{slug}/trusted_signers/{name}` — onboard (or
     replace) a publisher. Body carries base64(DER SPKI) — PEM
     armour is stripped at the CLI / handler boundary.
   - `DELETE /v1/apps/{slug}/trusted_signers/{name}` — offboard.
     404 is the canonical `trusted_signer_not_found` Problem so
     `gregale trusted-publishers remove` treats absent rows as
     idempotent success.

4. **Customer PATCH /v1/apps/{slug} silently drops `require_signed`.**
   The toggle is operator-only. The customer who can PATCH
   `require_signed=true` on their own app can immediately
   circumvent the gate they're turning on (they can pre-stage the
   trusted_signers table however they want), so the only legal
   path is the admin-scoped endpoint.

5. **imaged verifies at deploy time**, after `pg_notify` dispatch
   and before `PullDigest`. The verify path:
   - Loads `/etc/faas/secrets/trusted-publishers/*.pem` once at
     startup; refreshes on `pg_notify('trusted_signer_changed')`.
   - For each pending deployment, resolves the manifest digest,
     fetches the cosign signature blob at the registry's
     well-known `sha256-<digest>.sig` location, and verifies
     against every trusted publisher until one matches.
   - On `ErrSignatureMissing` (registry has no sig): marks the
     deployment FAILED with reason `signature_missing`; emits
     `app.signature_missing` audit event.
   - On `ErrSignatureInvalid` (sig exists, no match): marks FAILED
     with `signature_invalid`; emits `app.signature_invalid`.
   - On success: emits `app.signed_image_accepted` with the
     matched signer name + digest.

   Live enforce-mode images are revalidated at imaged startup and after
   `trusted_signer_changed`. If the current trust set no longer validates an
   image, imaged parks its deployment with the durable security quarantine
   reason, emits `app.signature_revoked`, and requires a newly signed image
   before recovery.

6. **apid pre-flight gate** rejects 403 `deploy_signature_invalid`
   before imaged ever sees the deploy, in two cases:
   - `app.require_signed=true` and `app_trusted_signers` is empty
     for this app (operator-on / no-publishers footgun).
   - `app.require_signed=true` and `req.RequireSigned=*false` (the
     customer is trying to weaken an operator-on flag for one
     deploy; operator policy wins).

7. **Default-off posture**. No production behaviour changes on the
   merged PR. Only opt-in customers (an operator flips the flag
   AND the customer is deploying OCI images, not source tarballs)
   see the gate. This mirrors issue #471's streaming PR-A pattern.

8. **Wire-shape parity** with AWS Lambda's TrustedSigners. Naming
   on the apid side uses `trusted_signers` / `signer_name` /
   `require_signed`; the on-disk mirror at
   `/etc/faas/secrets/trusted-publishers/` uses `<signer_name>.pem`
   so the file basename matches the apid-side label.

## Why not the full cosign CLI / sigstore-go bundle?

ADR-038 §Rejected alternatives already answered this for the
build side: spec §11 only requires a tamper detector on cold-boot,
and the digest+ECDSA primitive covers that with zero cosign-CLI
weight in the build tree. This PR reuses `pkg/cosign`'s verifier
(`verifyDigest`) directly, so the deploy-time signature is the
exact same wire format as the build-time layer signature (64-byte
P-256 r||s over the 32-byte SHA-256 of the manifest digest).
Rekor transparency log verification is out of scope per the
issue body; it surfaces as a follow-up if a customer asks for it.

## Wire format (operator contract)

The verify path expects a **raw 64-byte ECDSA P-256 r||s signature
over the 32-byte SHA-256 digest of the manifest** — NOT the
cosign v2 JSON envelope (Rekor bundle, plain sig, or certificate).

This is the same wire format as the build-side
`pkg/cosign/verifier.go::verifyDigest` (ECDSAP256Raw signing /
verification). The signature MUST be reachable via the OCI
content-addressed blob endpoint at the manifest's digest.

Operators MUST sign with a tooling that emits this raw shape — the
official `cosign sign` CLI emits the cosign v2 JSON envelope as a
*tagged* `sha256-<hex>.sig` artifact, which the platform's
verify path does NOT parse. Recommended signers (in priority
order):

1. **`cosign sign --output-signature=signature.sig --key <key>`** then
   push the signature blob with the manifest digest as the OCI tag
   (manually, e.g. `crane blob push <registry>/<image>@sha256-<manifest-hex>.sig signature.sig`).
2. **A custom OCI signing CLI** that emits the raw 64-byte signature
   blob and pushes it via the registry's content-addressed blob endpoint.
3. **`rekor-cli`** — see `rekor-cli` docs for the raw signature
   emission flow.

The verify path returns `ErrSignatureInvalid` (NOT
`ErrSignatureMissing`) when the signature blob is present but
fails ECDSA verification. Operators hitting this 403 should:

- Confirm the signing tool emitted raw r||s (not JSON envelope).
- Confirm the trusted publisher's public key DER bytes match the
  signing tool's `--key` argument.
- Confirm the signature was pushed to the manifest's digest path,
  not the cosign v2 `sha256-<hex>.sig` tag location.

This is a deliberate deviation from the cosign v2 standard. The
trade-off is documented in `## Why not the full cosign CLI / sigstore-go bundle?`
above — we keep the verifier primitive to keep the deploy-time
dependency surface minimal. A follow-up issue will add full
cosign v2 envelope parsing (with Rekor bundle verification) when
a customer asks for it.

## What this PR is NOT

- **Not KMS-backed signing.** ADR-039 / Phase 4 covers the
  KMS side; this PR uses the on-disk keypair that PR #371 ships.
- **Not a Rekor transparency log check.** Future follow-up.
- **Not a customer-facing change.** The wire-shape delta for the
  customer PATCH endpoint is purely additive (a new field that
  gets silently dropped); the customer-facing create-deployment
  path gains a 403 in two narrow operator-policy scenarios.
- **Not a source-tarball change.** The railpack path bypasses the
  gate by design (ADR-003 — builds run inside ephemeral builder
  microVMs, so the customer's host-side tarball is never signed
  and never needs to be).

## Consequences

### Compatibility

- Default off means no production behaviour change. The merged
  PR is invisible to existing customers.
- The `app_trusted_signers` migration is `IF NOT EXISTS` per
  table and `ADD COLUMN IF NOT EXISTS` for the apps flag, so
  the migration is replay-safe (memory `replay-safety contract`).
- imaged reads the trust list from disk at startup; missing dir
  is non-fatal (returns empty list, apid pre-flight gates).
- The wire adds 4 new error codes:
  - `deploy_signature_invalid` (403) — pre-flight gate.
  - `trusted_signer_invalid` (400) — PEM shape failure.
  - `trusted_signer_not_found` (404) — DELETE on absent row.
  - `plan_limit_trusted_signers` (403) — exceeds plan cap.
- The plan-cap surface mirrors `cron_limit_per_app`: Free 0,
  Hobby 4, Pro 8, Scale 16. Free keeps the "ship any image"
  posture; the cap is enforced inside the apid PUT handler so a
  Free customer never even reaches the on-disk write.

### Operational

- One new ansible directory at `/etc/faas/secrets/trusted-publishers/`
  (mode 0755 root:root for the dir; 0444 per-file for the PEMs).
- imaged's `FAAS_TRUSTED_PUBLISHERS_DIR` env var defaults to the
  canonical path; a non-canonical install can override.
- `gregale trusted-publishers add|remove|list` is the operator
  CLI; it uses the SDK's `PutAppTrustedSigner` /
  `ListAppTrustedSigners` / `DeleteAppTrustedSigner` methods.
- Audit log surface (issue #291 / ADR-035):
  - `app.security_updated` — admin toggled `require_signed`.
  - `app.trusted_signer_added` — admin onboarded a publisher.
  - `app.trusted_signer_removed` — admin offboarded a publisher.
  - `app.signed_image_accepted` — deploy passed signature check.
  - `app.signature_missing` — registry had no sig blob.
  - `app.signature_invalid` — sig exists, no trusted match.
  - `app.signature_revoked` — a live enforce-mode image failed revalidation
    after the trusted-publisher set changed; the deployment was quarantined.

### Risks (addressed inline)

- **R1. Two-step failure visible in operator UI.** A signature-
  invalid deploy inserts a `pending` row briefly before imaged
  marks it FAILED. The audit event + `failure_reason` column on
  the deployments table are the canonical surfaces; the
  apid pre-flight closes the no-trust-list case entirely.
- **R2. Stale in-memory cache after a notify drop.** Reread on
  every deploy from disk (cheap, file IO of 1-2 KiB) so a
  dropped `trusted_signer_changed` notify recovers on the next
  deploy rather than waiting for a restart.
- **R3. Registry without cosign signature layout.** Some self-
  hosted registries don't follow the OCI cosign convention;
  `ErrSignatureMissing` (not `ErrSignatureInvalid`) makes that
  case distinguishable in the audit log.
- **R4. Operator toggled the flag but forgot to onboard a
  publisher.** The apid pre-flight gate catches this with a
  descriptive 403 before imaged ever runs.

## Critical files

| Path | Operation |
|---|---|
| `migrations/00083_reserve_slot.sql` | new — slot reservation placeholder |
| `migrations/00083_apps_require_signed.sql` | new — ALTER apps + CREATE app_trusted_signers |
| `migrations/00083_apps_require_signed_test.go` | new — pgtest pattern |
| `pkg/e2etest/harness.go` | bump `e2eMigrationTarget` 80→83 |
| `pkg/state/types.go` | `App.RequireSigned bool` + `AppTrustedSigner` struct |
| `pkg/state/pgstore.go` | CRUD cluster for `app_trusted_signers` |
| `pkg/state/memstore.go` | MemStore parity |
| `pkg/state/store.go` | 4 new interface methods |
| `pkg/api/dto.go` | `RequireSigned` on Create/Update/App/Deploy + 5 new types |
| `pkg/api/errors.go` | 4 new error codes + 4 Problem constructors |
| `pkg/api/limits.go` | `TrustedSignerCountMax` per plan |
| `pkg/cosign/verify.go` | new — `VerifyImageSignature`, `TrustedPublishersFromDir` |
| `pkg/cosign/verify_test.go` | new — round-trip + missing + wrong-key + bad-perms |
| `pkg/db/notify.go` | 2 new notify channels (`trusted_signer_changed`, `audit_event`) |
| `pkg/imaged/handler.go` | verify hook + cache refresh + audit emit |
| `pkg/imaged/loop.go` | subscribe to new notify channels |
| `cmd/imaged/main.go` | wire `FAAS_TRUSTED_PUBLISHERS_DIR` env var |
| `cmd/apid/handlers.go` | createDeployment pre-flight signature gate |
| `cmd/apid/handlers_ext.go` | updateApp silently drops `require_signed` |
| `cmd/apid/handlers_security.go` | new — `patchAppSecurity` |
| `cmd/apid/handlers_trusted_signers.go` | new — list/upsert/delete |
| `cmd/apid/server.go` | mount 4 new routes (admin+MFA chain) |
| `cmd/apid/handlers_signature_test.go` | new — table-driven signature tests |
| `pkg/api/client.go` | 3 new SDK methods (List/Put/Delete) |
| `cmd/gregale/commands_trusted_publishers.go` | new — operator CLI |
| `cmd/gregale/main.go` | mount `dispatchTrustedPublishers` |
| `api/openapi.yaml` | 4 new routes + 5 new schemas + `require_signed` on 3 existing |
| `deploy/ansible/roles/imaged/templates/imaged.env.j2` | `FAAS_TRUSTED_PUBLISHERS_DIR` |
| `deploy/ansible/roles/apid/templates/apid.env.j2` | `FAAS_TRUSTED_PUBLISHERS_DIR` |
| `docs/adr/035-auth-audit-events.md` | append 6 new audit-event rows |
| `docs/faas_implementation_spec.md` §11 | add the trusted-publishers / require_signed paragraph |
