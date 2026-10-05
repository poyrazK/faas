# ADR-596: Immutable runtime releases and read-only upgrade previews

Status: accepted · 2026-10-05

## Context

Managed platform updates require knowing which runtime an artifact actually
uses. The source-declared language version and `BuildProvenance.BaseDigest`
(the builder VM image) cannot establish that identity. ADR-567 converges a
mutable logical base across nodes; replacing that base can change a later cold
boot of an older deployment. Gregale already has guarded rollouts, candidate
readiness, release handoff and rollback, so this change establishes exact
runtime identity before adding an update executor.

## Decision

Persist builderd's host-resolved `RuntimeBaseRef` with the fenced successful
build transaction, including cache hits. Dockerfile builds have no Gregale
runtime ref. Imaged consumes that exact recorded source when separating a
Node/Python OCI artifact from its base; daemon configuration changes between
build and imaging must not change the layer boundary. Go retains its existing
normalization of the customer executable.

For the six managed function families, publish immutable base generations with
runtime family, architecture, digest-pinned OCI source, injected guest-init
SHA-256, layout version and exact ext4 SHA-256. Hash that complete identity to
produce the release ID. Do not invent a language patch version or duplicate the
source version fields rejected by ADR-052. The host kernel and app-layer
function runner remain distinct components; existing backing identity checks
(ADR-510) still govern snapshot restoration.

Stage under a unique private key using the complete base OCI chain, without
replacing a logical base or borrowing a mutable parent. Validate the exact
source/guest-init sidecar, inspect base paths, hash the published bytes, retain
the existing fail-closed scan sidecar, then publish the immutable object before
its catalogue record. The runtime/architecture/source/guest-init/layout tuple
has one canonical generation: concurrent nodes may produce different ext4
bytes, but the first successful catalogue publication wins. Verify the winner
and use it rather than replacing it. Storage or database failures cannot
publish an artifact binding. Missing or corrupt catalogue bytes fail closed;
rebuilding nondeterministic ext4 cannot repair the same release identity.

For local split-box storage, hand off the immutable base, content sidecar and
scan sidecar before binding the application artifact. The configured helper
must support `--runtime-release BASE_KEY`; the bundled helper validates the
immutable key shape and all three source files before transferring them.
Legacy custom helpers fail closed until updated. Multi-node publication uses
the existing shared artifact backend so every publisher can fetch the winner.

Bind the physical application layer key and account to its release while the
matching deployment remains pending, building or imaging. A binding cannot be
changed to another generation. Prime, normal wake, migration and attached
application tasks resolve the same binding; qualification uses the shared
prime preparation. Promotions and environment clones reusing the same physical
layer within an account consequently retain the base without copying mutable
app defaults. Lookup failures cannot substitute a logical base.

Older artifacts and legacy builds without recorded evidence retain their
existing logical-base behavior and return **unknown** provenance. Do not
backfill from the current operator ref, builder image or source declaration.
Rebuilding records identity for a new artifact. Customer Dockerfiles, OCI
images, ordinary applications, standalone Jobs and sidecars are outside this first slice;
their runtime selection is not silently modified.

Expose account-owned read-only deployment runtime evidence and a target-ID
upgrade preview. Catalogue reads return at most 50 publications of the current
family and architecture. Public responses expose digests rather than operator
repository names. A different family, architecture or unknown current identity
blocks the plan. An identical target is `no_change`. Any different compatible
family/architecture is `review_required`, never a qualification verdict.
Publication order does not establish a newer interpreter patch or safe upgrade.

Execution is always unavailable. The preview requires native target
qualification, rebuilding the same source, a fresh cold boot and readiness,
then existing guarded rollout/rollback. It changes no VM, environment, traffic,
health observation or source. Historical app health (ADR-594/595) remains
advisory and cannot authorize an update. Expose this evidence in the typed API,
SDKs, CLI and the console's existing runtime tab.

## Consequences

The migration is forward-only: base catalogue records and artifact bindings
must survive to boot retained deployments. No release garbage collector is
introduced; immutable published objects and catalogue records are retained.
Private staging objects are cleaned after the publishing attempt; a failure or
losing concurrent publication may leave an unreferenced final object, which a
future reference-aware collector must handle. Never add these generations to
the logical-base convergence loop or application-layer cleanup.

The first generation can require a full base build; later identical input
publications reuse the canonical object. The catalogue is populated when
managed function artifacts are prepared, so preview targets must have been
published by an earlier candidate build. A future operator publication and
qualification workflow should precede customer update execution.

This implements the identity and planning foundation, not Beanstalk-style
scheduled updates. A future executor must pin source, target, configuration and
secrets, use existing candidate authority/readiness and rollout fences, and
retain the prior release for rollback. Kernel, function-runner, sidecar and
customer-image upgrades require separate component coverage.

## Validation

Memory/PostgreSQL tests cover canonical concurrent publication, immutable
artifact/account bindings and fenced build completion. Image tests cover
retained old generations, scan sidecars and missing/corrupt publication.
Scheduler tests cover prime/wake/migration base selection and lookup-error refusal;
local handoff tests require all base evidence and reject unsupported helpers. API
and CLI tests cover planning without mutation, unknown provenance and blocked
family/architecture changes. Console tests cover target selection, unknown and
empty states, cached-read failures and preview errors.

The VM lifecycle acceptance gates remain mandatory before deployment: run
`test-metal` and `leakcheck` on a dedicated native x86_64 KVM host. The local
macOS environment cannot satisfy them. The configured `faas-acceptance-1`
project was inaccessible during implementation (GCP reported its consumer
project suspended), so local tests do not claim native runtime qualification.
