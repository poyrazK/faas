# ADR-20261019120034: Controlled rollout of verified environment promotion

## Status

Implemented. Production admission remains off pending actual native execution,
supplemental qualification and explicit operator approval. No production approval
or signing key was created by this implementation.

## Context

ADR-20261019120023 through ADR-20261019120033 add verified HTTP, service graph, worker and migration
promotion with default-off, fleet-wide flags. CI emits commit-bound qualification
reports, but the deployed admission path did not require trusted evidence or
restrict those flags to a small cohort. The reports also explicitly exclude
additional runbook cases; a passing aggregate cannot imply those cases passed.

## Decision

Gate new verified admissions with `pkg/promotionrollout`. Keep the existing four
boot flags as prerequisites. Require an exact full source revision stamped into
APID, an operator-installed Ed25519 public key, a signed approval and a local
rollout policy. Missing, unreadable, malformed, untrusted, expired or mismatched
configuration refuses new admissions. Unknown or abbreviated build SHAs refuse
admission. Clean Makefile builds stamp the full revision; dirty/development
builds stamp `unknown`. Verified archive builders supply their checked revision.

An approval binds its source revision, issue/expiry times (at most seven days),
exact account/project pairs, capabilities, complete native report and supplemental
gate receipts. Every receipt names a passed gate and its reviewed evidence digest.
The trusted signer reviews artifact provenance and test results; a local JSON
file's `qualified` field is not independently authoritative. The signature covers
the complete embedded report, including the combined native log hash. The signing
command requires the matching log. APID checks the current complete report shape and test
inventory; it does not rerun CI or infer completion of supplemental tests.

All approvals require the current aggregate, extended to six partitions by ADR-20261019120035. Partial reports,
older inventories, failed reports, report errors and reports claiming automatic
enablement are rejected. CI contracts keep the runtime's required inventory in
sync with the report producer. Supplemental requirements are explicit:

| Capability | Additional evidence required beyond the native aggregate |
| --- | --- |
| `health` | `http-failure-variants`, `operator-intent-races`, `publication-crash-boundaries`, `retention-failures` |
| `service_graph` | All health gates plus `service-graph-routing`, `service-graph-recovery`, `service-graph-isolation` |
| `workers` | All health gates plus `worker-intent-races`, `worker-baseline-variants`, `worker-retention-expiry` |
| `migrations` | All health gates plus `migration-service-graphs`, `migration-resolve`, `migration-input-drift`, `migration-stale-dispatch`, `migration-vmmd-outage`, `migration-lane-races`, `migration-deadline-exhaustion` |

These IDs refer to the existing HTTP/worker and migration acceptance runbooks.
They are operator-reviewed attestations, not generated successes. Signatures do
not prove arbitrary SQL safety or replace build/artifact verification.

The local unsigned policy can pause admissions, remove capabilities or select a
subset of the signed targets. Both policy and approval must contain the exact
account/project pair; account-only or project-only membership never suffices.
Health is required by every extension. No wildcard or fleet-wide grant exists.
Both documents allow at most 128 targets and have bounded byte sizes. Trust keys
are pinned at startup; rotating trust requires restarting APID. Signing keys stay
off APID hosts.

Read policy and approval on every admission check with no last-known-good cache.
Reject duplicate/unknown/case-aliased JSON keys, absent required fields, symlinks,
special files and group/world-writable files. Linux opens are nonblocking and do
not follow symlinks. Preview includes an availability blocker; immediately before
creating a new operation, check again. Approval consumption remains in the existing
creation transaction, after this check. A request already checked before a policy
replacement can commit; the switch does not cancel in-flight accepted work.

Persisted operations, FIFO dispatch of already accepted waiters, idempotent
replays, reconciliation, abort, rollback and migration recovery use their existing
durable authorization. They never require a refreshed rollout approval. A pause,
expired approval, removed file, changed build or APID restart must not strand them.
Unverified legacy promotions and ordinary deploys retain their existing contracts.

`gregalectl promotion-rollout readiness` evaluates staged configuration locally,
reports every capability and missing-evidence reason, and exits nonzero when the
requested capabilities are blocked. JSON output and exit status use the same
observation. Explicit revision and boot-flag inputs describe the APID being
evaluated, not the operator CLI's own version. The command does not query or
change a running server. `keygen`, `sign`, `pause` and `resume` provide local
operator file operations. Pause/resume atomically replace the policy, preserve
Linux ownership/permissions and refuse an observed concurrent replacement.

The existing native promotion harness explicitly selects qualification-only
access. APID permits it only on Linux amd64 as root, with a root-owned designated
host marker, KVM and a local isolated `faas_test_*` database (not a template).
The flag alone cannot open a production database. This mode never produces a
production-ready readiness result. The outer native runner retains its existing
bare-metal, host isolation, source identity and leakcheck requirements.

## Consequences

Operators can review a concrete cohort and pause new work without changing
durable recovery behavior or putting a signing key on APID. Capability-specific
supplemental evidence prevents the new report from silently authorizing the
remaining unqualified service graph, worker and migration variants.

Portable tests cover signature, scope, expiry and evidence rejection, concurrent
policy replacement, CLI signing/readiness/pause and APID admission/recovery
behavior. Legacy and snapshot promotion regressions pass. The native-tagged
fixture compiles and its metadata test passes; runner, partition and report
contracts pass using synthetic evidence. These checks do not qualify VM execution.

Native execution and all supplemental gates remain pending. ADR-20261019120035 adds
executable HTTP/worker publication crash coverage without recording a native pass. Follow
[controlled promotion rollout](../ops/promotion-rollout.md) before provisioning
an approval or enabling any production flag.
