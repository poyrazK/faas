# ADR-946: Durable entity validator release tooling

Status: local implementation, unqualified; release gate default off.

## Decision

Extract ADR-944's validator registry encoding, integrity validation and digest
calculation into `pkg/durableentity/validatorbundle`. API startup and release tooling
use the same Go JSON digest representation. Existing registry wire format is unchanged.

Add `cmd/durable-validator-registry` as an offline release hook. It packages only
explicit source files from a reviewed directory, sorts file paths, rejects symlinks
and escaping paths, bounds bytes/files and calculates the bundle digest. Reads use
an OS root to prevent escape during a filesystem race. Source packaging does not
run customer code, extract a production rootfs, scan for secrets or automatically
include application configuration. Release tooling must provide a stable, reviewed
source directory without concurrent mutation. File order is part of the digest;
new tooling sorts it, while existing registry validation preserves its historical
encoding. Different bytes for an existing deployment identity are rejected. Identical
packaging is idempotent. A changed validator requires a new deployment identity.

An optional existing registry input is merged without dropping old bindings.
Entries are ordered by deployment identity, subject to the existing registry bounds.
The output is a new mode-0600 registry artifact published by linking a fully written,
synced temporary file; an existing output is never overwritten. Artifact publication
requires a filesystem supporting same-directory hard links. Release tooling must
serialize registry generation and stage the complete artifact through its existing
operator process. This hook does not copy files to remote hosts, restart daemons or
build interpreter binaries. Logs report digest/identity/file count, never source.

`FAAS_DURABLE_ENTITY_VALIDATOR_RELEASE_GATE_ENABLED=1` requires isolated restore
validation and applies only to allowlisted durable-entity applications. The shared
APID traffic preflight rejects deployments without an app-bound, intact validator
bundle. Explicit binding promotion, traffic redistribution and promote/advance
rollout recovery paths use this check even when OpenAPI contract diffing is off.
Traffic redistribution checks every gaining deployment, including predecessors.
The loaded registry is immutable during the process lifetime, so a checked binding
cannot be edited concurrently in that process. Source cases cover shared hashes,
registry merge identity, artifact protection and disabled-contract preflight.

## Boundaries

The operator must stage the artifact and restart every API writer before promotion;
this is release-hook automation, not automatic builder publication or dynamic registry
reload. The gate proves local loaded bundle availability, not fleet acknowledgement,
plan-specific resource admission or runtime host readiness. Initial builder publication,
scheduler-driven rollout/rollback, separate background recovery writers and direct
store writes do not gain this APID check. Do not treat the gate as universal deployment
safety; preload validators for every possible serving or rollback deployment. Full
builder/scheduler integration needs a persistent artifact contract across component
owners rather than forwarding a mutable APID startup map into those components.

Registry bounds still require an operator lifecycle policy. Do not remove a validator
needed for rollback just to fit a new release. Deployment selection and object-store
publication remain non-atomic as described in ADR-943/944. No tests, builds, live
provider/VM checks or actual registry installation were performed in this workspace.
