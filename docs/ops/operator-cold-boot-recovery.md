# Operator cold-boot recovery

Use `POST /v1/admin/apps/{slug}/force-cold-boot?confirm=true` with the existing
admin authorization and reason. Poll its returned operator-intent URL. A
successful result includes `cold_boot_generation` and the exact
`snap_ids_marked_stale` when snapshots existed. An empty affected-ID list still
establishes a durable boundary against delayed publications.

Success means the recovery policy, result and audit handoff committed together.
It leaves running instances serving. On the next new boot, the deployment uses
rootfs until a capture descending from the new generation is available. Older
warm/frozen candidates are retired when a wake considers them. A pre-existing
boot may finish, but its snapshots cannot become eligible after the boundary.
Healthy captures restore normally without an operator clearing a flag.

## Interrupted execution

- If the worker or database fails before commit, the intent remains running
  and snapshot policy remains unchanged. The existing five-minute stuck-claim
  reclaim makes it pending for another attempt. Each claim has a fresh UUID.
- If the commit acknowledgement is lost, retries return the original result.
  A restarted worker observes the completed intent. Neither case advances the
  generation again or invalidates a newer healthy capture.
- If audit delivery fails after commit, its durable outbox entry remains for
  the existing delivery worker. Retrying the action does not recreate a
  delivered/pruned handoff. Its original ID remains on the intent.
- A failed target lookup or account mismatch produces a failed result and an
  attributed audit. Transient database errors remain retryable; they cannot
  produce a successful empty result.
- A wake that selected a snapshot just before recovery may lose admission
  to the boundary. It fails before starting a VM; its next attempt cold-boots.

Use the existing trace ID to correlate the request, intent and outcome. Polling
does not expose execution claim tokens. Issuing a POST with a new request key after a completed
action requests another recovery generation. Reusing the original key replays
its acceptance receipt; see the [submission guide](operator-recovery-submissions.md).
Poll or replay the original intent
to recover its execution result. The legacy unary RPC has no durable invocation identity and should
not be used for recovering an uncertain reply.

## Deployment

1. Complete native `make test-metal` and `make leakcheck` qualification on the
   supported x86_64 Linux KVM host, including cold boot, fresh recapture,
   restore, warm-pool resume and frozen thaw after an operator recovery.
2. Pause cold-boot submissions and drain/stop old schedd executors and imaged
   publishers before applying migration
   `20261017100000001_operator_cold_boot_recovery.sql`. Keep existing generation
   zero snapshots. Deploy the scheduler, publisher and API from the same build
   before resuming submissions. Draining includes resolving pre-upgrade running
   cold-boot intents: the new protocol cannot reconstruct their original
   affected snapshot IDs. No vmmd wire protocol changes are required.
3. Verify one test action: its terminal generation is positive, both old tiers
   are retired, delayed old captures are rejected, a fresh wake cold-boots, and
   the subsequent healthy capture restores. Confirm the outcome audit and trace
   correlation.

Mixed old schedulers cannot supply new boot lineage or durable completion.
The database guard rejects their generation-zero publications after recovery;
this is a safe refusal, but they cannot restore healthy caching. A coordinated
rollout is required. Down migration refuses once a durable recovery boundary or
completion generation exists; use a forward fix to preserve that evidence.

Portable evidence and any remaining qualification are recorded in
[STATUS](../STATUS.md) and [ADR-1523](../adr/1523-durable-operator-cold-boot-recovery.md).
