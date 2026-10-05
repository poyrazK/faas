# ADR-599: Native qualification receipts for runtime upgrade targets

Status: accepted · 2026-10-05

## Context

ADR-596 publishes immutable runtime bytes, ADR-597 selects them for a build, and
ADR-598 retains reviewed source/configuration and the serving predecessor.
Publication and vulnerability scans do not prove that a selected runtime boots
and becomes ready on native hardware. Environment qualification receipts belong
to one deployment attempt and cannot qualify an unrelated platform base.

## Decision

Add a private operator-owned qualification receipt keyed by the complete runtime
release ID. The `runtime-upgrade-native-v1` contract requires a dedicated native
Linux/KVM host without nested virtualization, verified exact published base and
guest-init bytes, an artifact cold boot and readiness, passing `test-metal`, and
passing final `leakcheck`. The receipt retains the host and kernel-boot UUIDs,
native architecture, source/harness commit, kernel and Firecracker digests, and
digests of the aggregate report, test-metal output and leakcheck output, with the
acceptance interval and server recording time. Digests and commit identities use
fixed SHA-256/Git formats; no new free-text limits or quotas are introduced.

Only a trusted native acceptance owner may assert this profile after verifying
the physical host, test outcomes and artifact identities. Hashing a report or
supplying these fields does not itself prove acceptance. This slice implements
receipt persistence and consumption, **not** an importer, native harness or
customer-accessible qualification operation. No production caller records a
receipt yet. Unit-test fixtures are synthetic storage inputs, never native proof.
Both architectures can be represented; neither receives implicit qualification.
The repository's required deployment gate remains dedicated native x86_64 KVM.

Memory and PostgreSQL stores enforce one immutable receipt per exact release,
matching architecture and a completed acceptance interval. Repeating the exact
same input returns the original receipt and timestamp. Normalize acceptance
timestamps to PostgreSQL microsecond precision in both stores. Different report,
host, component or acceptance inputs cannot replace an existing receipt.

Revocation requires the expected aggregate report digest and a retained
revocation-report digest. It records a server timestamp and is permanent for
this receipt/release. Idempotent retries preserve the original revocation time;
neither re-recording evidence nor a deployment retry can undo it. Database
triggers retain receipts, prevent evidence changes/deletion, and forbid removing
or rewriting a revocation. A future audited supersession contract is required
to requalify identical bytes; this slice deliberately provides no reset switch.

Builderd and imaged require a readable, complete, compatible and unrevoked
receipt after resolving an explicit upgrade target and before building or image
preparation. Final image preparation repeats the check. Missing stores/receipts,
wrong target or architecture, unknown profiles and database failures block the
operation without selecting daemon defaults. Ordinary unpinned deployments
retain their existing behavior. Revoking upgrade eligibility does not tear down
the currently serving deployment or prevent its retained artifact from booting.

Classify receipts as platform configuration in the clone schema inventory.
They apply to physical platform releases and are not customer configuration or
per-environment approval records; clones do not manufacture new receipts.

## Consequences

Preparation checks are point-in-time observations. Revocation during a build
does not create a long-lived permission to activate it. The future apid cutover
must serialize its qualification check with revocation in its authoritative
write transaction and repeat baseline checks. A candidate still needs fresh
cold-boot/readiness evidence for its own artifact and inputs, guarded rollout,
and rollback. Kernel/Firecracker digests record acceptance context; they are not
yet a fleet compatibility policy or automatic qualification for changed hosts.

Public previews keep `execution_available=false`; no apply/maintenance controls,
traffic writes or native VM execution are introduced. The trusted importer,
native qualification runs and candidate/cutover authority remain subsequent work.

## Validation

Memory and real PostgreSQL tests cover missing evidence, incomplete profiles,
architecture/release mismatch, receipt immutability, recording/revocation races,
idempotent revocation and permanent quarantine. SQL tests prohibit rewriting or
deleting evidence and undoing revocation. Builder/image tests reject missing,
revoked, mismatched and unreadable receipts before materialization; existing
ordinary-build and exact-target tests remain applicable with synthetic receipts.
The migrated clone schema gate verifies the registered table and all columns.

Native `test-metal` and `leakcheck` remain mandatory before deployment. The local
macOS tests do not establish native runtime qualification. The dedicated
acceptance project remains suspended; no real release is qualified by this work.
