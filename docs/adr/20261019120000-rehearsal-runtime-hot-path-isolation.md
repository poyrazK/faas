# ADR-20261019120000 · Rehearsal runtime hooks stay off ordinary hot paths

- **Status:** accepted
- **Date:** 2026-10-19
- **Amends:** [ADR-20261017201005](20261017201005-rehearsal-network-containment.md) (rollout gate), [ADR-20261017201009](20261017201009-rehearsal-postgres-fixtures.md), [ADR-20261017201010](20261017201010-rehearsal-object-fixtures.md), [ADR-20261017201012](20261017201012-rehearsal-worker-termination.md), [ADR-20261018000200](20261018000200-rehearsal-postgres-fault-recovery.md)
- **Decision:** A node that has not enabled rehearsal containment makes no rehearsal database reads on wake, resume, outbound relay, identity or event-publish paths. Rehearsal dispatch pre-checks are advisory, and rehearsal sweeps never run on the scheduler loop goroutine.
- **Why:** As merged, every vmmd wired the durable rehearsal policy store, whatever the apid gates said. Every application wake and every resident resume (resume, thaw, quiet resume, warm-snapshot resume) for every tenant read Postgres first and failed closed. Before rehearsals, those resume paths were local. A database blip therefore became stuck-paused VMs fleet-wide while no rehearsal could even be enrolled. In the same way, a failed rehearsal pre-check before each async claim silently skipped every tenant's dispatch, and the rehearsal listings ran synchronously on the scheduler's main loop.

## Decision

1. **vmmd gate.** vmmd wires the rehearsal policy store only with `FAAS_REHEARSAL_NETWORK_CONTAINMENT=1`, the same flag apid uses for enrollment. Disabled, a wake carrying a rehearsal run ID is refused, because `applyRehearsalNetwork` cannot confirm a policy. The scheduler's checked runtime then destroys the VM, and the dedicated boot RPC reports the node not ready. Containment therefore still fails closed. Startup revocation of command-network identities stays unconditional, so a restart with the gate off cannot keep identities from an enabled incarnation.
   - **Rollout order:** set the flag on every vmmd that may serve rehearsal apps before setting it on apid. ADR-20261017201005 already required the complete serving fleet to run policy-reading runtimes before enrollment.
2. **Advisory dispatch pre-check.** `ScenarioWorkerDispatchBlocked` before an async claim is an optimization. The claim UPDATE's triggers enforce the same three holds: worker fault (migration 021), suspend/resume (024) and PostgreSQL fault (026). A failed pre-check read is logged and the claim proceeds under those guards, instead of skipping dispatch with no signal.
3. **Off-loop sweep.** The per-tick listings of pending worker faults and suspend/resumes run as one coalesced `rehearsal_sweep` loop-work task (one slot, drop on overflow), not on the loop goroutine.

## Related fixture corrections

- **Object collector.** A collector failure is sticky by design (ADR-20261017201010), but a pre-admission lookup that failed only because the caller abandoned its own request no longer trips it. Nothing durable exists before admission. Admission itself runs detached from client cancellation, so an ambiguous commit cannot journal an attempt nobody finishes.
- **PostgreSQL capture.** Capture stops reading once raw rows exceed `MaxEvidenceBytes`. That is four times the canonical bound, so every inventory that previously passed still passes. Previously, apid could buffer 16 tables × 1,000 rows × 64 KiB before the canonical check ran.
- **Fault locking.** Fault reconciliation, Seal and Delete serialize per run instead of service-wide. One run's capture and drop no longer hold another run's scheduled restore past its recovery budget. The backend already takes a per-database advisory lock and opens a connection per operation.
- **Reaping.** Expired PostgreSQL and object fixtures are selected in random order, so rows whose cleanup keeps failing cannot occupy every 16-row batch and starve newer expiries.

## Consequences

- Flag-off nodes have the pre-rehearsal wake and resume behavior. With the flag on, ADR-20261017201005's recheck-before-resume behavior is unchanged.
- Row triggers on `invocations`, `app_tasks` and webhook deliveries still run on every node. Each returns after one primary-key lookup on `scenario_test_action_policies`. Removing them needs a migration and is not part of this decision. The webhook delivery fence also locked the delivery's `apps` rows on every write; [ADR-20261024230731](20261024230731-rehearsal-webhook-and-dns-hot-paths.md) limits that lock to inserts and bounds the private DNS chaos read.
- Webhook capture registration (ADR-20261017201003) stays ungated. It affects only the caller's own fresh preview apps, and a gate would not remove the triggers above.
- Not changed here: retention of `scenario_*` rows, the purge guards that block app/account deletion while a fixture is undroppable, and the one-second fault reconcile tick.

## Rejected alternatives

- **Caching negative policy reads in vmmd.** Enrollment requires a fresh app, but a cache would make containment depend on cache invalidation rather than on the durable row.
- **Failing the advisory pre-check open without logging.** Operators need to see a database guard doing the work.
- **A keyset cursor for reaping.** It needs a catalog interface change for the same outcome as random selection over a small expired set.
