# Operator restart recovery

`force_restart` operator intents use [ADR-1522](../adr/1522-recoverable-operator-restart-execution.md).
Their plan and result survive a scheduler restart. This operation stops the
selected instance and invalidates its selected snapshot tiers; replacement boot
continues through the existing wake/service reconciliation paths.

## Read the evidence

Poll `GET /v1/admin/operator-intents/{id}` with an authorized admin credential.

| Status / execution phase | Meaning | Recovery |
| --- | --- | --- |
| running / prepared | Original target and snapshot invalidation committed; physical teardown has not been authorized. | Reclaim can resume the original plan after five minutes. |
| running / started | Vmmd authorized one teardown; the result is not yet durable. | Do not repeat the operation. The current attempt may still acknowledge it. |
| running / destroyed | Teardown acknowledged; database completion has not committed. | Reclaim finishes the database work without another VM call. |
| outcome_unknown / outcome_unknown | Teardown may have run; no durable acknowledgement exists. | Inspect the original node and physical generation before issuing any new action. |
| outcome_unknown / destroyed | Teardown is proven, but target/lifecycle validation or database completion still needs reconciliation. | Matching RUNNING/STOPPED targets are recovered by the dispatcher; changed/missing targets remain paused. |
| succeeded / completed | Teardown acknowledgement, STOPPED, outcome and audit handoff committed. | Retrying completion recovers the original result. |
| failed / absent or failed | VM teardown did not start; any snapshot invalidation remains recorded. | Correct the input/node capability before explicitly issuing a new intent. |

`snapshot_ids` identifies exactly what preparation invalidated. A later attempt
does not invalidate snapshots created after that plan. `destroyed_at` is populated
only by vmmd after its teardown boundary succeeds; absence or a stopped instance
does not prove which operation stopped it. `finished_at` on `outcome_unknown`
records when automatic execution was paused, and clears if reconciliation claims
the command again.

An acknowledgement can arrive after the scheduler records uncertainty. It is
retained independently of that scheduler attempt. A matching original target is
then claimed to finish database work; this does not authorize another destroy.
The audit history retains both the uncertain outcome and the later success.

## Resolve uncertainty

`gregalectl instances force-restart` exits with status 1 when the outcome is
unknown, preserving the intent/status URL for investigation. Its JSON output
includes the execution evidence. It neither keeps polling until a timeout nor
submits another destructive action automatically.

Use the original `node_id`, `wake_id` and `generation` when examining vmmd logs
and its existing native process/resource recovery evidence. An RPC timeout may
leave cleanup running. A different current generation, changed migration history (even after returning
to the same node), migrated placement or
deleted instance cannot be inferred to be the original operator action's result.
Keep the uncertain intent as evidence and issue a new operator action only after
the original physical outcome has been established. Do not set `destroyed_at`,
change the phase or clear the journal to make a retry possible. There is no
automatic fallback to the ordinary Destroy RPC.

Host-port leases are released with the proven STOPPED transition, rather than
by a later cleanup call that could reach replacement leases. Admission remains
reserved while the durable state transition is uncertain.
The normal instance/admission reconcilers may repair capacity from authoritative
state; an uncertain command alone is not permission to release resident capacity.

## Rollout

1. Quiesce operator restart submissions/dispatch and drain existing running work.
2. Apply `20261017090000001_operator_restart_execution.sql`. Existing running
   restarts become `outcome_unknown`; reconcile them before a new action.
3. Upgrade vmmd with its durable state store and correct local compute identity.
   The default-local path uses the seeded compute-node UUID too. Nodes without
   the capability reject the new RPCs before authorizing a teardown.
4. Replace all schedulers together; old scheduler writers bypass the protocol.
5. Run portable and native lifecycle acceptance before enabling production use.
   Resume submissions and check operator polling and the audit outbox.

Do not downgrade while execution evidence or unresolved legacy restarts remain.
The migration refuses that loss. Preserve the journal through any separately
reviewed retention/export procedure. No deployment or rollout is performed by
adding these files.


## Native qualification

On the supported native KVM acceptance host, stage `FAAS_TEST_KERNEL`,
`FAAS_TEST_BASE_ROOTFS`, `FAAS_TEST_LAYER_ROOTFS` and the matching
`FAAS_TEST_FC_VERSION`, then run the new generation fence test:

```sh
make test-metal PKGS=./pkg/fcvm RUN_REGEX='^TestMetalOperatorRestartGeneration$'
make leakcheck
```

Run the existing teardown/native process recovery suites and the current full
native acceptance gates as well. A compile or a skipped test does not qualify
physical VM teardown. The managed development environment compiled the new
metal test but skipped execution because the staged images were absent; it
also has no `/dev/kvm`.
