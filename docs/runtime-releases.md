# Runtime identity and upgrade previews

Gregale-managed function artifacts can record an immutable runtime base release.
The identity covers the OCI source digest, architecture, guest startup binary,
base layout and exact published base bytes. The host kernel and function runner
are separate components. A source language declaration or builder image digest
does not establish the deployed runtime identity.

Inspect one deployment:

```sh
gregale deployment runtime v42 --app my-function
# Machine-readable evidence:
gregale deployment runtime v42 --app my-function --json
```

The console shows the same evidence under **Release details → Runtime**. An
older artifact without a recorded binding shows **unknown**. It retains its
legacy logical-base behavior; a fresh deployment records a binding. Ordinary
apps, customer images/Dockerfiles, standalone Jobs and sidecars are outside this first
slice. No inferred runtime identity is offered for them.

Choose an exact published candidate from the returned catalogue to preview:

```sh
gregale deployment runtime v42 --app my-function --target RELEASE_ID --json
```

The catalogue contains at most 50 recent publications for the current family
and architecture. Candidates appear after a managed function build publishes
them. A publication is not a tested upgrade and its position in the catalogue
does not prove a newer language patch. The preview compares component identities
and describes blockers, required rebuilding, fresh cold boot and rollout steps.
Changing families or architectures requires an explicit application migration.

The preview changes no deployment, traffic, source or environment. Applying and
scheduling updates are not implemented yet. A future updater must qualify the
target, rebuild the same source, obtain fresh candidate readiness, and use the
existing guarded rollout and rollback. Health history is useful for
investigation but cannot itself authorize an update. Older snapshots cannot be
reused across different backing bytes; existing snapshot checks enforce this.

The backend now has the internal build preparation contract for choosing an
exact target before queueing. The pin records the source archive checksum,
build root, handler and selected runtime ID. Builderd uses that release's OCI
source; imaged reuses its exact base bytes even if the installed guest-init or
daemon defaults change. Failed-build retries retain the same target. Missing
or mismatched source, runtime or scan evidence blocks preparation. This
contract does not authorize activation, so the API, CLI and console continue
to offer read-only previews. See
[ADR-683](adr/683-runtime-upgrade-build-targets.md).

Internal preparation can now capture the serving artifact and configuration
and secret-version fingerprints before queueing an explicit zero-traffic
candidate. Build and image preparation refuse a changed serving deployment,
configuration or secret input. Retries retain the original review; they cannot
silently adopt new values. Secret values are not copied or restored by this
record. Activation still needs target qualification, fresh cold boot/readiness
and baseline checks inside guarded cutover. See
[ADR-684](adr/684-runtime-upgrade-baselines.md).

REST reads: `GET /v1/deployments/{id}/runtime` and
`GET /v1/deployments/{id}/runtime/upgrade-preview?target=RELEASE_ID`.
Both require read authorization and deployment ownership. See
[ADR-682](adr/682-immutable-runtime-releases.md) for publication, retention and
native acceptance requirements.

Runtime metadata bounds are 1,024 bytes for the physical artifact key, 64 bytes
for the layout identifier and 4,096 bytes for reading the source/guest-startup
sidecar. Local split-box installations must update their artifact handoff helper
to support `--runtime-release BASE_KEY`; the bundled helper transfers the base
and its content/scan evidence before the deployment can use it. Multi-node
publication requires a shared artifact backend.

Internal upgrade build pins limit each captured source root and function
handler to 4,096 bytes. Pinning is unavailable after a build is queued or an
artifact is materialized; selecting another runtime requires a fresh attempt.

Explicit upgrade preparation now also requires an unrevoked native qualification
receipt for the exact target. Publication and scans alone cannot authorize a
build. The private operator importer verifies a separately trusted signature,
exact native metal coverage, published artifact binding/bytes and retained
readback evidence before recording a receipt. It provides no customer apply or
scheduling operation. See [ADR-685](adr/685-native-runtime-release-qualification.md),
[ADR-686](adr/686-trusted-runtime-qualification-import.md) and the
[native qualification runbook](ops/runtime-release-qualification.md).

Qualification evidence bounds are 64 KiB per report/envelope/fixture JSON,
64 MiB per captured log, 256 KiB per log event/line and 16 JSON nesting levels.
The profile currently requires dedicated native Linux amd64 KVM. The private
[guarded collector](adr/687-guarded-native-runtime-qualification-collector.md)
now stages exact committed source and published artifacts, captures native
test/leakcheck evidence, restores the acceptance host, signs and imports only a
successful retained bundle. It uses independent operator pins and a protected
signing seed, never customer API input. Actual designated-host acceptance
remains outstanding; macOS unit fixtures never qualify a release.

Native collection bounds are 2 GiB per staged asset and 512 MiB per source
archive, with a 15-minute exclusive lock wait, 100 ms lock polling, 10-minute
source/build preparation budgets, 3-minute test budget, 2-minute cleanup budget
and 5-second command pipe wait delay. Failed cleanup or restoration retains
staging for operator recovery and blocks another collector attempt.

Internal candidate prime now retains a fresh cold-boot/readiness receipt for
the exact rebuilt layer, runtime release, admitted instance/node/wake and guest
configuration/secret fingerprints. Publication rejects a changed baseline or
revoked runtime qualification. A read-only validator checks those fences again
and limits evidence to 15 minutes from cold-boot dispatch. Retries require new
acceptance; restore and historical health evidence cannot supply it. This proves
readiness at publication, before subsequent snapshot/hosting/rollout gates.
It enables no customer apply. See
[ADR-688](adr/688-runtime-upgrade-candidate-acceptance.md).

The private apid state seam now provides atomic cutover enforcement. Inside
one traffic transaction it rechecks the retained serving baseline, current
configuration/secret versions, exact acceptance wake and artifact, freshness
and unrevoked target qualification. It records the acceptance used, moves the
candidate to 100% and retains the previous live artifact at zero for rollback.
Generic traffic and rollout SQL cannot give a pinned candidate its first live
traffic without that cutover record; failure fallback excludes unactivated
candidates. A retry confirms historical completion and never reapplies a
rolled-back update. This state seam has no customer caller. Native end-to-end
acceptance and gateway acknowledgment/drain remain outstanding, and
`execution_available=false` stays unchanged. See
[ADR-689](adr/689-atomic-runtime-upgrade-cutover.md).

The private apid executor now journals a reviewed, pre-uploaded zero-weight
candidate with its target pin and baseline. It queues one stable build, waits
for the existing build/image/fresh-prime pipeline, and commits cutover with its
completion checkpoint. Expiring fenced leases recover abandoned work; lost
responses cannot duplicate a build or undo rollback. Input/qualification drift
blocks the operation with a fixed code. The operation has a 30-minute deadline,
30-second lease and five-second polling interval. Customer controls and
production worker deployment remain outstanding; the executor is private and
disabled during normal daemon startup. See
[ADR-690](adr/690-durable-private-runtime-upgrade-executor.md).

Private source staging now copies the exact retained serving archive, verifies
its recorded size and SHA-256, and publishes the candidate spool and configured
source object before registration. Corruption or storage failures cannot fall
back to mutable Git. Lost registration responses return the retained operation
without republishing source. A separate opt-in `apid --runtime-upgrade-worker`
mode supervises database polling with capped backoff and watchdog progress; no
service unit or customer admission enables it. Public customer controls, handoff cleanup, gateway convergence/drain and
dedicated native end-to-end acceptance remain outstanding; ADR-692 extends
reservation and private recovery below. Public previews still report
`execution_available=false`. See
[ADR-691](adr/691-verified-runtime-upgrade-source-and-private-worker.md).

Private admission can now atomically reserve its zero-weight candidate, target,
reviewed baseline and non-executable operation before source I/O. Identical
retries retain the same inputs; source verification promotes the reservation
only after rechecking its original baseline and qualification. Failed uploads
remain retryable; abandoned reservations expire at the original deadline.
Private account-scoped status omits lease tokens, spool paths and values.
Cancellation fences stale workers and fresh cutover attempts, cancels build and
release-command work, and durably queues running-build cleanup through existing
owners. A committed cutover remains historical completion; a live zero-traffic
cancelled candidate remains retained pending owner-controlled cleanup. These
seams have no public routes and leave `execution_available=false`. See
[ADR-692](adr/692-atomic-runtime-upgrade-reservation-and-controls.md).
