# ADR-947: Shared durable entity validator artifacts

Status: local implementation, unqualified; default off.

## Decision

Add a private object-storage artifact backend shared by release tooling, API writers,
imaged and schedd. `FAAS_DURABLE_ENTITY_VALIDATOR_ARTIFACTS_ENABLED=1` selects it;
legacy startup registries remain available when this gate is off. API startup requires
the entity preview, isolation and validator release gate together when shared artifacts
are selected. Configure all lifecycle owners consistently before enabling any writer.
No production configuration is enabled by this change.

Store compact JSON validator content (`runtime`, `entrypoint`, `files`) under an
app-scoped SHA-256 key in `gregale/durable-entity-validators/v1/`. Store a separate
immutable app/deployment binding to that digest under the same versioned prefix.
The binding is durable bucket metadata, not a new deployment SQL column or an atomic
transaction with SQL traffic selection. An app/deployment identity cannot be rebound.
The trusted release publisher creates content conditionally, verifies it independently,
then conditionally creates the binding. It never overwrites either object. Identical
retries confirm stored bytes after conflicts or lost acknowledgements; different bytes
fail closed. A cancelled/failed read cannot prove an uncertain publication succeeded.
Partial publication can leave an unreferenced content object; it cannot create a binding
to unverified content. Multiple deployment identities can reference the same app's digest.

Every resolution performs a bounded strict metadata read followed by a bounded content
read and digest/bundle validation. Cross-app scope, malformed identities, missing,
corrupt, unknown-field and altered content fail closed. Shared mode has no fallback
into the process-local registry. Code changes become visible without restarting an API
writer. Existing restore deployment/digest pins and receipt identities are preserved;
committed replay skips validation and therefore needs no artifact fetch.

The existing offline release hook gains `--publish-registry`. It uploads a reviewed,
packaged registry to the private backend without executing validator code. It reports
per-deployment completion so interrupted batches can be retried identically. This is
an explicit release pipeline hook; builderd does not automatically discover validator
source, infer dependencies or scrape a production rootfs. Keep reviewed validator
packaging and publication before candidate priming in the release pipeline.

schedd deployment priming verifies the shared binding before instance allocation or
VM startup. imaged verifies it again before initial live publication. APID traffic
promotion/redistribution and canary advance use the existing shared preflight. Explicit
rollback selection (including rollback-on-5xx), exact abort, alert rollback and service
recipient/checked-rollback worker checks use the same resolver. All checks apply only
to the configured durable-entity app allowlist. Ordinary wakes of already-serving
apps do not depend on validator bucket availability. No direct VM calls or lifecycle
ownership bypass is added. Private runtime execution remains scheduler-owned.

## Configuration and retention

The shared provider is explicit `gcs` or `s3`, using native conditional-state storage.
Native GCS uses normal ADC and optional service-account impersonation. S3 uses dedicated
validator credentials delivered in the existing per-daemon secret EnvironmentFiles.
Artifact configuration and the app allowlist are declared in the daemon environment
contract, including prior APID preview settings needed for startup resolution.

Content remains bounded by the existing 4 MiB registry and execution bundle limits;
metadata reads use the existing small verdict limit. Resource admission still applies
when a validator actually executes. Provider qualification is required: source integrity
checks do not prove conditional writes, durability or private ACLs on a live bucket.

There is intentionally no artifact deletion, expiration or rebinding API. Retain all
bindings and content required for serving, rollback and audit investigations; prohibit
bucket lifecycle deletion of this prefix. Orphan reclamation and eventual reference-aware
retention need a separate contract. Do not mistake a content digest for proof of code
purity or strip secrets embedded in source/candidate state.

## Limits and qualification

Readiness is a preflight rather than an atomic SQL/bucket transaction or fleet
acknowledgement. Bucket deletion, credential loss and inconsistent app allowlists can
invalidate later checks. Direct store writes and separately implemented publication
paths (including project/environment cloning) must not be treated as covered by these
hooks; qualify each enabled release path before rollout. This change adds no universal
storage-level SQL publication fence.

Source cases cover uncertain upload recovery, independent readers, changed bindings,
missing/tampered scope, no local fallback, pre-prime refusal and pre-live refusal.
Tests, builds, daemon contract checks, live provider permissions, native isolation and
end-to-end release acceptance were not run in this workspace. No live artifacts,
credentials, deployment settings or pull requests were changed.
