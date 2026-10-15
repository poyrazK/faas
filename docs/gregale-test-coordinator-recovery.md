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
