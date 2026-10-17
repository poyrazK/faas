# ADR-992: Combined managed PostgreSQL rollout readiness

- **Status:** accepted
- **Date:** 2026-10-12

## Context

Provider qualification v8 proves provider behavior and durable SQL/secret
recovery. The thirteen-phase deployed workload suite proves guest deployment,
restored pools, scheduler retirement and cleanup using simulated management
and real SQL. Neither result alone establishes complete rollout readiness.
The existing provider verifier and guest verdict are separate; compiled tests
and a nested-node diagnostic must not be mistaken for guest execution evidence.

## Decision

**2026-10-16 update:** the report envelope remains version 1; its required guest
suite is now version 4 with twenty phases covering both shipped starters and
Node frozen-thaw recovery. The Node PostgreSQL SDK adapter and
shipped starter add pre-checkpoint draining and same-guest frozen-thaw recovery.
The shared contract and strict verdict reject earlier reports. These additions
need fresh supported native execution before they provide rollout evidence.

Keep provider v8 usable for isolated staging diagnosis. Add guest evidence v1
and a combined read-only result with separate `provider_lifecycle` and `guest`
results. The combined `ready` value requires both. Verification performs no
provider requests, SQL connections, VM operations or provisioning changes.

Fresh provider output also records the qualifier executable's clean Git revision
from Go build metadata. An additional provenance digest covers the provider
report, durable lifecycle, spec, approval and placement. Combined verification
requires that revision to match the approved guest bundle. Earlier v8 reports
without this additive provenance remain provider diagnostics and cannot supply
combined readiness. Updating provider code without changing its configuration
fingerprint therefore cannot silently reuse an earlier report for a new release.

The guest report requires:

- One complete Go JSON execution of the selected package, root and all phases
  required by the current suite, without failed/skipped descendants, duplicate completion or missing
  package completion. Shared contract JSON defines phase and artifact names.
- Native Linux amd64, a clean checkout, the actual SQL major, bounded execution
  times and an expiry (24 hours by default, at most seven days).
- Before/after host leak checks and successful removal of the runner's owned
  proxy address and archival of its private transcript before publication.
- The checkout revision and SHA-256 of every harness daemon/helper, test
  executable, guest init, kernel, builder base, Firecracker, jailer, and both
  workloads' published drive0 bases. Go executables must carry matching clean
  native VCS metadata, including reused `FAAS_E2E_BIN_DIR` binaries. Runtime
  bases are read from the deployment's actual runtime-release binding and their
  physical bytes must match the published base/guest-init identities. Artifacts
  are checked again after workload shutdown.
- A transcript digest and a digest of the bounded report. Free-form output,
  filesystem paths, credentials and provider identities never enter it.

The runner forces `-buildvcs=true` for test and harness builds. Qualification
executables, including prebuilt guest init and the provider qualifier, must be
built in a regular clean clone whose `.git` is a directory; the pinned Go
toolchain does not discover Git worktree metadata files. General reproducible
production builds deliberately disable VCS stamping and cannot provide these
qualification executables. Build metadata is checked rather than inferring
executable provenance from the caller's checkout.

Reports are published privately, atomically and without overwriting existing
evidence. Nested reports remain usable diagnostics but cannot return guest
readiness. The expected qualification-bundle manifest is separately approved
by the operator. It identifies test-tagged binaries from the intended release
revision; its daemon hashes do not describe the differently tagged production
executables. Copying the report's manifest without checking the intended bundle
does not independently establish that the release was tested.

`managed-postgres-qualify --verify --rollout` reuses provider v8 validation and
compares the guest report with the expected bundle and provider's tested SQL
major. Missing, expired, mismatched or incomplete evidence returns stable
blocking codes and a non-zero exit. Supplying either guest input implicitly
selects combined verification, as does the daemon's guest requirement policy.

Before a staging canary, set
`FAAS_MANAGED_POSTGRES_REQUIRE_GUEST_QUALIFICATION=true`, the provider approval
path, guest report path and expected manifest path. The existing staging gate
then requires combined readiness. Either guest path opts in even when the
boolean is false; malformed or partial configuration cannot fall back to the
legacy environment approval. Files are loaded at startup, with expiry checked
on every admission; replacement requires restart. Provider-only diagnostic
staging remains available without this opt-in. Production environment admission
continues to be closed.

## Validation and limits

Pure race regressions exercise complete/missing evidence, provider and runtime
changes, expiry after daemon startup, canary mismatch, partial-policy fallback,
nested classification, malformed documents and CLI blockers. A Python-produced
report is verified by Go, retains no raw output and cannot overwrite an earlier
report. Guest compilation does not count as guest execution.

These are operator-owned evidence documents, not signed remote attestation.
An untrusted/root-compromised host can invent logs or recompute digests. A clean
designated native host and operator custody remain necessary. The fixture does
not qualify provider DNS/Internet paths, optional snapshot/copy protocols,
every region or every PostgreSQL major. Combined readiness does not enable
production, change account entitlements or complete restore cutover activation.
