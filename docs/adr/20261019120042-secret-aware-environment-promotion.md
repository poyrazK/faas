# ADR-20261019120042: Destination secret changes during HTTP environment promotion

## Status

Implemented in the control-plane storage, API and CLI paths. Production admission
remains behind existing boot flags and an explicit `secrets` operator rollout
scope. Native guest qualification has not been executed in this workspace.

## Context

ADR-20261019120020 introduced release-scoped secret changes for atomic source deployments.
ADR-20261019120022 environment promotion inherits production secrets while reusing tested
artifacts. A release that changes production credentials needs a way to promote
those artifacts and select new destination secret versions in one operation.

## Decision

`gregale projects environments promote PROJECT --from staging --to production
--sync-config --verify --yes --release-secrets-file production.json` accepts the
same workload-to-complete-secret-map file as atomic source deployment. The CLI
uses the existing authenticated encrypted staging endpoint, then requests a
bounded metadata-only `POST .../environments/production/promotion-preview` with
the immutable version references. `--release-secrets-request-id` replays the
printed staging request within its existing 24-hour window. There is no source
upload, artifact rebuild or staging-secret transfer.

The first milestone supports complete HTTP projects and existing
application-owned persistent production secret keys. Each mentioned workload
replaces its complete serving secret map, including explicit empty maps; omitted
workloads inherit their effective production versions. Workers, migration commands,
managed credentials, explicit secret references/grants, sidecars and reload hooks
are refused. Existing document, workload and plan secret limits apply.

Preview requires MFA and secrets-write permission. It reports secret key changes
and previous/proposed immutable version IDs without plaintext, ciphertext,
recipient IDs or value fingerprints. The opaque promotion token carries version
references; the promotion identity binds those versions and the exact production
baseline. Approval and initial execution recompute the preview. Approval cannot
authorize a different rotation or an empty secret map. Initial execution also
requires secrets-write permission; already admitted reconciliation and recovery
use the durable capture.

Operation creation validates ownership and staging freshness under existing
project, environment, app and deployment locks. In that transaction it saves
encrypted destination secret captures and freezes any legacy previous delivery
onto the retained deployment. Candidate creation pins the proposed versions in
the same transaction as its deployment/environment/workload checkpoint. Every
workload participates, including unchanged artifacts and inherited secrets.
Staging expiry or pruning after admission does not invalidate recovery.

Existing destination probes gate publication. Graph/configuration publication
selects deployments with their corresponding secret pins. Final validation checks
candidate ownership, exact versions and logical secret material; wrong or missing
candidate pins prevent cutover. Rollback selects the retained original deployments
and secret versions together. Operator configuration edits retain the existing
conflict fences and are never overwritten by automatic restoration.

Encryption maintenance visits every sealed `secret_snapshot` and `secret_target`
in saved promotion captures. Retirement counts include these captures, and the
same retirement lock and compare-and-swap rules guard their writes. Preview
identities omit physical envelopes; a deterministic logical identity freezes a
legacy baseline without making recipient rotation invalidate approval. A migration
extends the existing maintenance view and immutable-capture trigger; downgrade
refuses to discard these guarantees while secret-bearing captures exist.

## Validation and rollout

Memory and PostgreSQL contracts cover rotation, inheritance, explicit removal,
version ownership, missing keys, stale previews, atomic baseline freezing,
candidate tampering, staging pruning, key rotation, rollback and operator edits.
HTTP contracts cover authenticated scopes, approval binding and admission;
CLI contracts cover the full staging/preview/approval/submit sequence, privacy,
invalid options and servers omitting the requested secret contract.

No new boot flag is introduced. Admission requires
`FAAS_PROJECT_RELEASE_SECRETS_ENABLED=1`, verified-promotion availability, and a
signed operator approval containing `secrets` with reviewed source-bound evidence
for `promotion-secret-cutover`, `promotion-secret-rollback`,
`promotion-secret-recovery` and `promotion-secret-key-rotation`. Existing health
or source-secret qualification alone does not grant this scope.

Before enabling production admission, use a dedicated native x86_64 KVM host to
observe different staging and production credentials; promote existing artifacts
with a destination-only rotation; prove dark-candidate and retained-client
delivery; restart APID/schedd/vmmd before preparation and after cutover; force
preflight and post-cutover regressions; restore the previous graph/configuration/
secret set; rotate envelopes and remove the old identity before cold boot and
rollback; finish with leakcheck. Storage tests do not establish guest delivery.

This operation does not create or revoke credentials at external providers. Old
external credentials must remain valid throughout the rollback window.
