# Migration promotion native acceptance

Run on the designated native x86_64 Linux KVM hosts with root and `/dev/kvm`,
using an isolated project and destination database. Cloud unit/PostgreSQL tests
exercise intent and receipts; they do not qualify guest execution.

The executable `promotion-migrations` partition of
[`project-environment-promotion-only`](environment-promotion-native-acceptance.md)
implements the single-HTTP-app success, rollback, nonzero-after-expansion and
uncertain-result/resume cases under ADR-20261019120033. It uses real source/destination TLS
databases and counts every command invocation, including a duplicate that fails
DDL. Native execution remains pending. Multi-service graphs, the resolve action,
drift, stale dispatch/RPC races, unavailable vmmd, competing release-mode lanes
and full deadline exhaustion below remain additional requirements.

1. Enable promotion health and migration admission. For a two-service graph,
   also enable service graph promotion. Prepare complete pinned source and
   destination graphs with database-aware critical HTTP health endpoints.
   Declare one source release command that adds a nullable column and records a
   unique execution marker. Use distinct source/destination database credentials
   and a destination migration credential unavailable to ordinary HTTP VMs.
2. Preview `--sync-config --verify --migrate`. Verify one command fingerprint,
   destination binding identity and exact protected-environment approval. Change
   the command, probes, source graph or destination credentials after preview;
   submission must reject the stale identity. No SQL should have run.
3. Promote. Observe dark candidate preparation, one queued release task, the
   migrating phase, a zero exit, then baseline/candidate preflight. Verify the
   destination database alone received the expansion, the marker appears once,
   ordinary VMs lack migration credentials, and traffic changes only after two
   healthy rounds. Verify post-cutover checks and the retained task receipt.
4. Lose APID responses and restart APID while queued, running and after task
   completion. Repeat the same idempotent request. The task ID and SQL execution
   marker must remain unique. Disable migration admission after acceptance;
   existing work must finish while new admissions stay blocked.
5. Kill a scheduler during restore before running. Reclaim within the one-minute
   start window; require the new lease to attest runtime again. The old lease
   must fail dispatch. Repeat after the running fence with no exit receipt; the
   operation must fail uncertain, preserve the baseline and refuse replay or a
   competing migration. Inspect the database and task logs before recovery.
6. Rotate destination credentials or edit runtime inputs between restore and
   dispatch, and between task success and cutover. Dispatch/cutover must stop.
   Race an ordinary release task, atomic deploy migration and schema contract
   admission with promotion admission. At most one migration lane may proceed.
7. Return nonzero after a partial expansion, omit a result, and exceed the start
   or execution deadline. None may publish a candidate. Verify bounded failure
   and clear task/status evidence without credentials in API/CLI responses.
8. Break candidate HTTP health after expansion, then after cutover with a healthy
   baseline. Verify preflight preserves the baseline; post-cutover restores the
   old graph and configuration. The new column and one execution marker remain;
   rollback must not run another release command or any reverse SQL.
9. Exercise native cold boot and snapshot restore with destination generated
   bindings. Before migration success, private candidate service graph routing
   must remain closed. After success, preflight must address the complete dark
   graph and the independent baseline graph.
10. Run the repository's metal and leak checks. Retain task IDs, exit receipts,
    phase/generation history, database marker counts and redacted VM/runtime
    evidence. Any ambiguous result keeps migration admission disabled.

## Operator recovery qualification

Run both recovery actions with the migration admission switch subsequently
disabled. Record node, task, dispatch lease and runtime UUID. Restore reclaims
must use different runtime UUIDs; old dispatchers without registration must fail
the running fence.

1. Lose a running command result, then terminate schedd. Submit resume with database
   inspection evidence. A replacement schedd must read the persisted runtime
   identity and destroy exactly that VM through vmmd before acknowledging teardown.
   Block vmmd access: recovery must stay quiescing without probing or cutover.
2. Restore access and confirm the guest/cgroup/TAP/netns is gone. Resume must retain
   the original uncertain receipt and run complete baseline/candidate preflight
   and post-cutover checks. The SQL marker must remain unique.
3. Delay an old restore and execution RPC across teardown and lease replacement.
   Verify that lease-specific VM identities and the running fence prevent a late
   command from targeting a replacement VM or executing after recovery.
4. Rotate runtime credentials/configuration before resume verification. It must
   block without reopening the promotion. Inspect, repair, and publish a separate
   verified nonmigration promotion; resolve must require fresh probes of its active
   graph. Switch graphs or rotate a member credential during the probe round:
   the transaction must reject that evidence.
5. Replay identical acknowledgements after later destination changes; the exact
   stored receipt must return. Change task, generation, evidence hash, action or
   replacement under the same request ID: conflict. Run concurrent recoveries and
   check one admitted decision. After resolution, a new explicit migration may be
   admitted while the original task can never return to running.
6. Exercise failed teardown, absent runtime metadata, stale task/generation,
   failed probes, verification timeout, restart after teardown acknowledgement and
   restart after recovery commit. No case may fabricate a zero exit, discard task
   history, replay SQL or implicitly revert schema changes. Run metal and leak
   checks on the designated hosts before enabling migration promotion admission.
