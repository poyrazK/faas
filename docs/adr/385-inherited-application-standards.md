# ADR-385 · Versioned inherited application standards

- **Status:** implementation in progress; acceptance required before release
- **Date:** 2026-09-30
- **Decision:** Add organization-owned immutable application-standard versions,
  scope assignments, automatic application enrollment, effective configuration
  with field provenance, bounded approved exceptions, and durable reviewed
  rollouts. apid owns intent; existing gateways, imaged, schedd and vmmd retain
  their enforcement and lifecycle ownership.
- **Why:** Per-app controls otherwise require copied configuration and manual
  enrollment. Company logging and security requirements should follow service
  ownership, survive every deployment entry point, and update through a visible,
  recoverable process.

## Contract

Standards contain an explicit supported vocabulary: logging destinations,
signature requirements, security posture, trusted publishers, outbound CIDRs
and extra outbound ports. Each field declares `default`, `mandatory`, or
`restricted` mode. Defaults yield to permitted local intent; mandatory values
cannot be weakened; restricted sets allow a subset. Logging can explicitly
permit additional destinations. More-specific assignments cannot weaken an
ancestor's mandatory constraint. Conflicting requirements fail with field-level
provenance. Unsupported controls and unknown JSON fields fail validation.

Versions are immutable and hash canonical definitions. Assignments select an
explicit admission version; publishing a version never silently activates it.
Apps inherit through persisted organization/project/app ownership. Scope
selection is not based on caller-controlled labels or the active API-key org.
The shared selectors distinguish admission from existing adoption. New services
use explicit admission versions; existing services resolve only their saved
adoption pins. A retained assignment disabled for admission can still govern an
existing service during its controlled removal. UUID spelling changes between legacy memory fixtures and
PostgreSQL cannot reset an adoption. Selected definitions are checked against
their canonical hashes and owning organization before the resolver receives them.
Creating apps, project reconciliation, GitHub deployment and clones share the
same enrollment boundary. Local intent is retained separately from the effective
projection. Mutations of managed settings pass the shared resolver.

Organization roles receive dedicated read/manage/approve actions. Owners and
admins manage standards; developers and viewers can inspect effective settings.
Exceptions name an app, version, field, replacement, reason, approving identity
and expiry. They cannot bypass platform restrictions or plan entitlements. An
expired exception is ineffective at admission even if repair has not run yet.
Credentials use organization-owned destination references and sealed storage;
definition, diff, audit and rollout responses contain no credential material.

The legacy project account scope must be verified against every participating
app's persisted organization; cross-organization project assignment is denied.
App-wide controls affect all deployments of that app. Production-only named
environment standards require explicit environment-aware enforcement; the API
must reject unsupported scope/control combinations rather than quietly affecting
staging. Application-level production scope remains supported.

App creating-account attribution and deployment app identity are retained.
Organization/project moves reenroll the existing app; cloning for a different
creating account creates a new app. Artifact and control writers participate in
advisory input fences so approval can take a stable cut without adding reverse
parent-row waits to legacy child updates or deletion.

## Updates and recovery

A preview captures target membership, current adoption, effective/local values,
exceptions, artifact evidence and the target standard hash. Approval is bound to
that complete plan. Apply locks and recomputes its inputs; changed inputs return
a stale-plan conflict without writes. Per-app checkpoints and a fenced worker
lease make batch operations resumable. New apps use the assignment's explicit
admission version during a rollout. Rollback previews restoration against current
platform restrictions and other mandatory standards; it is an audited operation.

Logging converges through existing drain delivery, egress through schedd's live
network repair and next-wake configuration, and image security through admission
and artifact verification. Desired, persisted and observed versions are separate.
An operation is complete only when its defined verification gates pass; absent
consumer acknowledgment is pending, never fabricated success. Durable change
records repair missed notifications. Partial convergence is visible and pauses
the next batch. No direct apid-to-vmmd calls and no hot-request inheritance query.

## Acceptance checklist

- [x] PostgreSQL and MemStore versioning, tenancy, immutable hashes and parity.
- [x] Resolver defaults, required settings, narrowing, conflicts and provenance.
- [ ] Organization authorization, API, SDK, CLI, generated contract and docs.
- [ ] Org/project/app assignments and automatic enrollment on every create path.
- [ ] Required log destination creation, protected mutation and credential safety.
- [ ] Image signatures/trusted publishers and current artifact verification.
- [ ] Outbound restrictions, runtime convergence and native network acceptance.
- [ ] Approved exceptions, revocation/expiry, repair and admission enforcement.
- [ ] Exact-plan approval, batch rollout, pause/resume, restart fencing, rollback.
- [ ] Effective-state and affected-app views with desired/observed adoption.
- [ ] End-to-end multi-service onboarding and controlled standard-update scenario.
- [ ] Product registry, operational guide and recovery evidence.

This ADR records the complete intended feature. Individual green tests do not
declare the feature launched or satisfy the entire checklist.

### Enrollment evidence

The storage boundary now captures admission pins at every app insert, including
raw project/reconcile/preview inserts, and revalidates restore and scope changes.
Deployment insertion is fenced in PostgreSQL as well as MemStore while enrollment
is pending, applying or blocked. Project-row membership locks prevent an
activation from overlooking a concurrent member owned by another organization.
Assignment identities are fixed, updates require the next revision, and direct
deletion is fenced except through owning-organization erasure. Tests cover both
orderings of the project membership race, foreign tombstone restore, transaction
rollback, immutable adoption reads and candidate publication without activation.

Private persisted assignment reviews now bind affected service inputs, scoped
membership, local values, resources, account entitlements and artifact metadata
to an approval digest. Freshness probes reread storage; they cannot be used as
authority for an unlocked later write. Reviews validate admission combinations
for future services and empty projects, preserve explicit defaults/logging extras,
and reject aggregate quota or unverified artifact changes. Tests exercise
PostgreSQL/MemStore parity, input mutation, cross-organization scope denial,
immutable operation history and account/organization erasure boundaries.

Private atomic approval now advances the explicit admission pointer and saves
the operation, frozen per-app inputs/projections and audit in one transaction.
Current actor authorization is required even for an idempotent retry. Expiry,
staleness, blockers and overlapping operations cause no intent writes. Parent
input fences cover controls, account quota, artifact children and retained live
artifacts; bounded nonwaiting lock acquisition avoids legacy lock-order cycles.
Tests cover both project membership orderings, duplicate approvals, injected
audit failure rollback, existing-pin preservation and restore lease revocation.
No approval advances a persisted or observed application revision.

The private PostgreSQL/MemStore materializer now claims operations with expiring
worker generations and installs each frozen target atomically into actual app
settings, log drains and trusted signer rows. Logical resource bindings keep
physical identifiers out of inheritance; private ciphertext backups preserve
legacy controls for reviewed removal. Installation rechecks the approved app,
current entitlements and resolver projection. Managed writes are protected at
both store and raw SQL boundaries, with nonwaiting child-row acquisition.
Persistence advances only persisted revisions. A later wave waits for actual
observation; no consumer acknowledgment is fabricated. Tests cover all six
controls, sealed baseline restoration, stale inputs, lease replacement, restart
wave gates, raw SQL protection, child-row contention and transaction rollback.

apid now runs bounded repair passes over durable reviewed operations and automatic
enrollments. Automatic installation resolves captured adoption pins, checks
current creating-account entitlements and fences authority by generation, desired
revision and storage-owned expiry. Reviewed queued targets take precedence.
Reenrollment retains last-installed field context, so project removal restores
original controls and restore cannot turn an inherited default into local intent.
Tests exercise PostgreSQL/MemStore onboarding, detach, restore, blocked recovery,
review precedence and worker replacement; PostgreSQL expiry after physical writes
rolls the entire installation back. Persisted installation does not acknowledge
any runtime consumer.

The durable schedd egress consumer now sends the complete CIDR/extra-port tuple
and its actual revision through a separate `UpdateAppEgressPolicy` RPC. Old
nodes refuse the method before network mutation. New clients require an exact
echoed revision before recording success. Within a vmmd process, a cancellable
per-app gate orders writes and denies older, conflicting or legacy updates once
a revisioned policy has been accepted. Failed physical writes retain the newer
accepted intent for retry. Wakes share that gate, use the latest accepted tuple,
and publish before an update enumerates live instances. Different apps and
ordinary simultaneous wakes still progress independently. A live plan that
cannot represent the complete port projection is rejected rather than silently
truncated and acknowledged. Unit and wire tests cover these orderings and
failures; the native network gate remains open.

The revision cache is process-local. Boot/migration admission and vmmd restart
freshness still need revision-bound evidence. Existing native CIDR patch failure
recovery, established-connection tightening and host firewall reload outcomes
must be verified before these RPC acknowledgments can advance standard rollout
observation. This addition does not advance any standard observed revision or
enable public activation. Upgrade vmmd before deploying the revisioned schedd
consumer; unsupported nodes remain pending rather than receiving an unversioned
fallback.

Public review/activation, permitted local intent mutations, exceptions and
consumer observation are still pending.
Schedd now checks the persisted enrollment envelope before wake reuse, new
admission, explicit-deployment prime/smoke, warm-pool creation, restart, app-task
runtime setup and migration-spec construction. Pending, applying, blocked,
missing or mismatched scope/revision envelopes refuse admission. An unavailable
reader fails closed. A valid persisted projection may boot to obtain actual
consumer evidence; these reads never fabricate observation. Tests use the real
reviewed MemStore admission and automatic materializer, including a restored app
that still has a running instance. Denied paths perform no VM lifecycle work.

These read guards do not close the concurrent change window after a spec is
captured. Raw instance/state-transition fences, revision-bound boot/migration
tickets, post-boot publication checks and already-cached gateway routing remain
required. Current image proofs and existing-runtime behavior remain part of the
runtime acceptance work before public activation is enabled.

New wake instances now capture persisted standards intent, app controls,
account entitlements, log destination/auth hashes, signer fingerprints and
deployment/sidecar-layer identities in the same transaction as their admission.
Nonwaiting parent and advisory fences retain that input cut through commit.
PostgreSQL raw state/runtime writes and the MemStore refuse publishing a capture
whose covered inputs changed. Captures are immutable and erased with their
instance; managed legacy instances cannot obtain invented historical captures.
Cleanup remains possible while intent is pending, and billing grace retains
serving eligibility. Wake, prime, warm preparation and migration-spec construction
also compare the scheduler's earlier app/account/artifact reads with the capture.
Prime now publishes runtime and RUNNING through the checked atomic CAS, destroys
a refused VM and releases its ledger reservation before returning an error.

This is a control-plane input capture, not a complete boot ticket or native
receipt. It does not cover the separate app-task runtime table, all boot-spec
fields, expired exceptions, vmmd incarnation/egress revision freshness, native
enforcement, content-bound image proofs, existing cached gateway routes or
consumer observations. A valid raw state transition still cannot attest to
native enforcement. Native revision-bound wire admission and its receipt,
existing-runtime convergence and the remaining acceptance gates are required
before public activation. No capture advances an observed standard revision.
