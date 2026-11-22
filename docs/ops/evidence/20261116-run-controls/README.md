# Run customer attribution, budgets and history

The [functional KVM report](report.json) passed on the selected
`gregale-internal-test-1` node in `gregale-prod`. The final source manifest is
`908b512feece17b5dd87810a6496e6cd57820aea9c37851b3d15582e312c0522`
(55,536 files), based on `e74c7cfc6e5b9cff5596d7f430a4d5e0c6bbfab1`.
The race-instrumented final journey passed in 51.54 seconds. Before result-note
updates, the local tree matched every file in that manifest exactly.

One actual Node 24 report task produced one Run and one durable RAP inbox
continuation. Its owned endpoint supplied authenticated customer/agent
attribution. With schedd stopped, a second call received structured customer
and task budget refusals and created no terminal inbox result. The test disabled
the customer policy, retired the task and disabled task writers, then restarted
apid and replayed the admitted call. After schedd restarted, the original Run
completed with a valid structured result and a stored report artifact. Fresh
scoped readers fetched both. Another apid restart and replay preserved one Run
and one inbox record.

Customer and task usage agreed: one invocation, zero active slots, zero
reserved compute and 89,760 MB-ms used. Filtered metadata history retained the
customer, agent, task version and valid result-contract status. The guest ran
as UID 1000 with a cold-boot witness. Pre-teardown and initial/final physical
leak checks found no leaked netns, taps, jails, cgroups, processes, bridges,
mounts or native loops.

The [local checks](local-checks.json) cover memory/PostgreSQL admission races,
compute reservations, key rotation, raw-source bypass refusal, cross-version
task limits, UTC accounting, missing-ledger conservatism, durable threshold
delivery, rollback and history authorization/pagination. Go SDK wire contracts,
8 Node SDK tests, 3 Python SDK tests and 19 acceptance-verdict tests passed.
The final validator rewrite also passed an independent database character/length
contract. Spec projection, SDK coverage, migration/schema and repository policy
checks passed. The local PostgreSQL fixture was stopped with its data retained.

All five [Linux lint groups](linux-lint.json) passed with zero issues against
the final source: state, API server, CLI, domains and metal acceptance. The
record retains an earlier combined load interrupted before scratch exhaustion
and the validator readability finding fixed before the passing checks.
The broader ADR-number uniqueness gate still reports two collisions already
present, unchanged, in HEAD and the turn baseline: `20261104120000` and
`20261111130000`. The new Run ADR is unique; no unrelated decisions were renamed.

The [qualification attempts](attempts.json) retain the initial passing journey,
the failed final-source fixture placement and the passing real-directory retry.
The failed placement introduced a symlink in the private snapshot-root path;
vmmd correctly rejected it with ELOOP before becoming ready. No customer Run
was admitted in that attempt, and both physical leak checks passed. The retry
kept the runtime path guard intact.

[Storage receipts](storage-maintenance.json) preserve exhaustive byte/mode
verification for closed generated executable archives and the obsolete approved
source upload retired to a private archive. The operator separately approved
evicting exactly 84 old generated Go package archives (6.073 GiB). Their hashes,
metadata and active use were rechecked; no candidates were skipped. Source,
module-cache, database and evidence files were excluded from that eviction.
Raw logs, final binaries, archives and full private receipts remain on the node;
the final journey is under
`/mnt/e2e-scratch/run-lifecycle-controls-20261116-preview3`.

This is functional evidence on an operator-selected Google KVM fixture with an
explicit test-only stock-runtime scan fixture. It does not confer native
production runtime eligibility. Customer model invocation, Sandbox/OCI budgets
and model-token spending are outside this lane. Controls cover source Runs and
named tasks, and their compute allowances retain the existing usage ledger.
See the [guide](../../../run-controls.md) for accounting and rollout boundaries.

Runtime, test and CI files match the final tested manifest. Subsequent edits
only record results and clarify rollout accounting in repository documentation.
Public records remove machine/boot identifiers and exclude signed callback
URLs, credentials, source payloads and raw daemon logs. New writers remain off;
no production flags, database roles or grants were changed, and nothing was
committed, pushed or deployed.
