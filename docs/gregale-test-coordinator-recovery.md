# Recovering a campaign after coordinator failure

New native campaign runs persist the coordinator identity in `runner.lock` and
a separate, lock-bound identity for every worker. Linux identities include the
boot ID, PID namespace and process start time; PID reuse cannot make a live
replacement process appear to own the campaign. Worker launch intent is synced
before starting the process. An interrupted identity write stays unverifiable.

Inspect the original local work directory:

```sh
gregale test policy recover-lock --plan plan.json --directory campaign-current
```

The JSON response reports `alive`, `stopped`, `terminated` or `unverifiable`
for the coordinator and workers. Inspection never removes the lock. Stopped
processes can resume, so they block recovery just like live processes.

Once the coordinator and every recorded worker are verified terminated:

```sh
gregale test policy recover-lock --plan plan.json --directory campaign-current --execute
```

Execution returns 2 when recovery is blocked, 1 for invalid evidence or an
operation error, and 0 after releasing the lock. Recovery first durably halts
the checkpoint and converts running batches to uncertain, then removes
`runner.lock`. Completed reports, journals and attempted cost remain intact.
Recovering the lock does not kill processes, delete sessions or retry tests.
Use the existing recovery assessment, journal cleanup and reconciliation flow
before creating a continuation.

A killed coordinator can leave a live worker behind. Allow that worker to
finish, or stop it through normal process supervision, before attempting lock
recovery. There is no force-unlock option.

Legacy empty locks, missing worker identities, another host or PID namespace,
unreadable procfs and unsupported operating systems remain unverifiable.
Process records are local supervision metadata and are not transferred as
portable campaign evidence. An imported work directory uses its own local
process records and still requires the active handoff index for mutation.

The portable recovery gate includes coordinator SIGKILL acceptance cases with
a surviving worker and with a killed worker. It verifies that a live process
blocks recovery, read-only inspection preserves the lock, confirmed termination
permits release, and uncertain work is never implicitly repeated.

## Recovering shared handoff claim locks

Imported runs, cleanup, reconciliation, re-export and runner-lock recovery now
persist a claim-lock owner identity and operation target before mutation. To
inspect a claim and every registered work directory together:

```sh
gregale test policy recover-claim --handoff recipient/bundle-index.json
```

Inspection is read-only. Add `--execute` to release exclusion only after the
claim owner, all recorded runner owners and all their workers are verified
terminated. A live, stopped, foreign-host, legacy or incomplete identity blocks
release. The command uses the same Linux process-domain verifier as
`recover-lock`; there is no force option.

While the shared claim lock still excludes other recipients, recovery durably
halts mutable checkpoints, marks running batches uncertain, and removes their
verified stale runner locks. It removes the shared claim lock last. Checkpoints
already sealed for reconciliation or frozen for transfer retain their exact
bytes. Claim contents, recipient nonce, registered work, transfer status,
receipts, journals and attempted cost reservations are preserved. Recovery
cannot activate a second recipient or return execution authority to a
transferred owner. A frozen export can be republished through the normal export
command after lock recovery.

A cleanup process can die after the platform has accepted a deletion. Lock
recovery does not repeat deletion. The normal journal cleanup command rechecks
session identity and absence before deciding whether another DELETE is needed.
A run interrupted before its final report remains incomplete after cleanup;
reconcile its evidence before creating a continuation.

The recovery gate includes actual owner-kill cases during an imported run,
cleanup blocked at the deletion API, and re-export blocked after publishing its
frozen bundle. It also checks legacy/foreign owners, nonce mismatches, missing
worker identities, and live runners in other registered work directories.
