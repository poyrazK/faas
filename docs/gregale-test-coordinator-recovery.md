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

## If the recovery process itself crashes

On Linux, recovery guards now use kernel advisory locks on stable files. Process
exit, including SIGKILL, releases the exclusion automatically. The marker files
remain in place: do not delete or replace them. Their stable inode prevents two
recoverers from locking different files, and their presence blocks older binaries
that used file creation alone for exclusion. Empty or unrecognized legacy guard
files remain unverifiable and block recovery.

Before changing any checkpoint, recovery durably records a transaction receipt:
`runner-recovery.json` in the work directory, or `<claim-path>.recovery.json`
next to the shared claim. The receipt binds the operation, original lock hashes,
checkpoint hashes and expected checkpoint contents. Recovery halts mutable work
before unlinking its exclusion locks, and removes the claim lock last.

Retry the same command with `--execute` after an interrupted recovery. Retries
recheck current ownership. If the final lock was already removed, completion is
reported only when the bound receipt's checkpoint and lock postconditions still
hold. A replacement lock, changed checkpoint or changed claim blocks replay;
recovery never restores old checkpoint contents to make a receipt match. Frozen
checkpoints and transferred authority remain unchanged. These local receipts are
not test results and are not included in portable bundles. Receipts are bounded
to 8 MiB; recovery rejects larger transactions before changing checkpoints or
exclusion locks.

The portable gate kills recovery subprocesses after preparing the receipt,
persisting the checkpoint, unlinking the runner lock and unlinking the claim
lock. It verifies automatic guard release, safe completion on retry, repeatable
completion checks, preservation of assertion evidence, and rejection of new
owners or changed checkpoints.

## Native crash/transfer acceptance

`make native-scenario-restart-acceptance` now requires a campaign crash drill
in addition to the existing 38 scenario cases. It uses the candidate CLI and
TCP dependency campaign, waits for actual fault matches and verifies both
journaled sessions, then kills the coordinator. Recovery must refuse release
while the tracked worker remains alive. The harness pins that worker with a
Linux pidfd after checking its boot, PID namespace and start identity before
killing it, so a reused PID cannot receive the signal.

After explicit lock recovery, the saved attempt must remain halted and
uncertain without a final report. Both source and activated recipient must
refuse to resume it. The drill exports the journal, activates a recipient in a
private shared claim directory, and performs scoped journal cleanup there.
Read-only completion verification must report absent sessions while execution
and overall recovery remain incomplete. An independent API probe checks all
recorded session names and app IDs against live instance inventory.

Evidence lives under `campaign-recovery/`: command results and private logs,
fault-match observations, the portable bundle, recipient, cleanup observations,
and a verdict. Namespace removal is recorded separately: journal cleanup does
not revoke scenario registrations or consumer credentials. On failure the
harness stops its pinned worker and attempts authorized journal cleanup, retains
the artifacts, and records `cleanup_pending` when cleanup cannot be completed.

Portable tests exercise process identity pinning and rejection of replacement
sessions or forged completion. They do not certify native VM recovery. The full
gate still requires the designated native x86_64 KVM host and its configured
dedicated acceptance account; a cloud workspace without KVM cannot qualify it.

## Recovering fault registrations and consumers

New campaign journals record registration intent before the namespace API call,
and consumer creation intent before provisioning. They retain workload app IDs,
consumer IDs, app slugs, run-specific external references and requested key expiry
times. They never store consumer keys or run secrets. Namespace registrations
also persist the member session deadlines captured by the server at registration.
The run expires at the latest known member deadline. Extending a session lease
does not extend that captured deadline, but a live member always blocks removal.
Deleting expired sessions alone does not prove that registration rows are gone.

The existing preview janitor sweeps a bounded number of expired registrations
after every member app has been deleted. PostgreSQL rechecks the discovered
membership and deadline under the same run lock used by registration and manual
cleanup, and locks member apps before removal. Busy or changed runs are skipped
for a later sweep. The sweep never deletes sessions or revokes consumers.
Explicit cleanup can still remove a verified namespace before its deadline.

The expiry migration captures existing finite member session deadlines; missing
deadlines and registrations from older server binaries remain `unknown` and
require explicit cleanup. Deploy the migration before the updated API. Its read
response adds `expires_at` and `expiry_state` (`pending`, `expired`, `unknown`, or
`absent`). Cleanup and completion observations include
`registration_expires_at` and `registration_expiry_state`; an older API without
expiry metadata is reported as unknown. Expiry is informational and never
substitutes for fresh membership/session absence checks or execution evidence.

After verifying all workers have terminated and recovering their locks, run:

```sh
gregale test policy cleanup --plan plan.json --policy policy.yaml \
  --manifest gregale-test.yaml --directory campaign --batch coverage-batch-1 \
  --journal --faults --execute
```

Omit `--execute` to inspect without mutation. Imported work also requires its
active `--handoff` index. The existing work-directory and shared-claim locks still
apply. Cleanup verifies the fault inventory, revokes exact recorded consumers,
deletes verified workload sessions, and then conditionally deletes the namespace.
Changed consumer ownership or external references, changed namespace membership,
unresolved consumer creation, failed lookups and legacy journals without a fault
inventory block this operation. Session cleanup without `--faults` retains its
existing behavior. Retry the same command after an interruption; immutable IDs
and live checks make already revoked/absent resources safe to revisit.

### Recovering an interrupted consumer creation

New campaign workers save a `creation_intent_sha256` with the expected app ID,
run-specific external reference and fixed credential expiry before requesting a
consumer. The dedicated test-consumer PUT commits that intent and the consumer
in the same transaction. Repeating it returns the original identity; a different
expiry, unreceipted conflicting identity, or changed ownership is rejected.
The server uses the existing run lock to serialize creation with registration
cleanup. The response cache is not the durability boundary.

If a response is lost or the final journal save is interrupted, cleanup uses the
read-only test-consumer lookup to recover the ID. It verifies the receipt version,
intent hash, expiry, UUID, app, external reference, name and ownership before
revocation. Recovered IDs appear in `consumer_ids` in fault observations. Frozen
journals and ancestor evidence remain unchanged. This works through portable
handoffs because the saved intent contains no host paths or credentials.
Intent-scoped revocation checks the same receipt and the expected consumer ID
atomically. It remains available after app soft deletion, when the ordinary
slug-based consumer route is unavailable. Linked or replaced ownership blocks it.

A schema-2 missing receipt is reported as pending. Executed cleanup durably
cancels its intent, atomically revoking any consumer that won the creation race.
Cancellation and creation share the run lock; a late creation request conflicts.
Read-only inspection and completion accept only a bound schema-2 cancellation
proof as cancelled. A lost cancellation response can be retried or resolved by
lookup. Unavailable routes, older missing receipts, changed intents and legacy
uncertain creation without an intent still require manual inspection. Lookup never creates
a consumer, and expiry never proves that an uncertain creation is absent. Normal
recovery never replays a fixture or mints a credential. An explicit provisioning
retry retains its original expiry and cannot reactivate a revoked consumer.
Standalone runs without campaign journaling retain their existing creation path.

Apply the consumer-intent and consumer-cancellation migrations and deploy the updated API before running
new campaign workers. Intent receipts remain with the consumer through revocation
and soft deletion of its app, and disappear on the existing hard-delete cascade.
Cancellation records survive namespace cleanup and app soft deletion and cascade
with hard app/account deletion. Update workers with the API for schema 2 support.
Raw keys are never saved in journals, receipts or observations.

The new registration read API returns versioned membership even after its apps
are inactive. Empty membership is an explicit successful response, so an old
server's unsupported route cannot masquerade as absence. Conditional deletion
compares the exact membership digest and requires every member app to be deleted.
Postgres serializes registration and deletion for the run within transactions;
in-memory state applies the same checks under its mutex. Deploy this API before
using fault cleanup. An unknown precondition is never sent to an older server
without first verifying its read capability.

Use `recover --inspect --verify-completion --check-cleanup --verify-faults` with
the same plan, policy, manifest, directory and handoff arguments to add current
fault-resource evidence to completion verification. `fault_resources_verified`
indicates the full requested inventory was verified; `fault_resources_absent` requires every
journaled consumer to be revoked/inaccessible and every namespace to be absent.
Failures keep overall recovery incomplete. These checks never create missing
execution reports, restore spent budget or waive recorded fixture failures.
Live VM retirement remains independently checked by the native acceptance gate.

Fault cleanup observations are stored separately as `fault-cleanup-*.json`.
Portable bundles carry the fault inventory in their existing hash-bound journal
snapshots; recipients need this CLI version. The native crash/transfer drill now
requires fault cleanup and fault completion checks as well as session/VM absence.

## Fault cleanup across recovery history

The active owner can now inspect or clean all attempted generations with:

```sh
gregale test policy cleanup --plan continuation-plan.json --policy policy.yaml \
  --manifest gregale-test.yaml --directory active-work \
  --journal --faults --fault-history --execute
```

Omit `--execute` for inspection. Do not supply `--batch` in history mode. Imported
work requires its active `--handoff bundle-index.json`. Cleanup uses the active
work directory's runner/shared-claim locks and registered continuation ownership.
It never reopens a frozen ancestor for execution or rewrites ancestor checkpoints,
reports, journals, inventories, or ownership ledgers.

History follows verified checkpoint/reconciliation links, up to 32 parents.
Portable parents resolve only through the import index; original sender paths
are never consulted. Every attempted batch requires a complete fault journal.
Finalized batches also require their unchanged bound report and matching workload
identities. Legacy boundaries, missing inventories, unfinished workers, ancestor
runner locks, duplicate run IDs, extra portable snapshots and replacement owners
block the operation. The combined inventory is bounded to 1024 workload/run
identities and 64 MiB of journal evidence.

All generations are inspected before the first mutation. Cleanup revokes recorded
consumers, deletes only verified sessions, then conditionally removes their fault
namespaces. It rechecks the graph and journal snapshot between phases. Failed or
interrupted operations remain safely retryable; progress comes from live API
checks. Observations are written only in active work as
`fault-history-cleanup-*.json`, with source plan/checkpoint/journal hashes. These
local observations are not execution receipts or portable authority.

`recover --inspect --verify-completion --check-cleanup --verify-faults` now
checks the same full history automatically. Its `fault_sources` and
`fault_history_snapshot_sha256` describe the verified inventory, and ancestor
sessions are rechecked before reporting session absence. A live or unresolved
generation keeps overall recovery incomplete. Missing execution evidence,
recorded cleanup failures and spent budget retain their existing meaning.

Re-exporting a recipient retains ancestor journal inventories without duplicating
inventory files. Subsequent recipients can verify and clean the full graph even
when the original machines and earlier recipients no longer exist.

### PostgreSQL acceptance

Run `make test-gregale-recovery-postgres` with `DATABASE_URL` pointing to a
local disposable PostgreSQL cluster. The target includes the portable gate and
requires both end-to-end race orderings, with and without API restarts, to pass; missing infrastructure or a
skipped acceptance test fails the gate. `FAAS_PGTEST_TEMPLATE_DATABASE=1` enables
the existing private database clone harness and requires database creation rights.
The ordinary `make test-gregale-recovery` remains available without PostgreSQL.
CI runs the PostgreSQL target against its own PostgreSQL 16 service.

The CLI acceptance binary starts the real authenticated API test server in a
separate process with an isolated, migrated PgStore owned by the parent test. A delivery proxy holds a
creation request and discards selected successful responses, while every durable
effect still goes through the production API handlers. It covers lost consumer,
key and cancellation responses, and deterministically exercises creation before
cancellation and cancellation before creation.

The test uses the public bundle export/import and cleanup commands. It imports
a three-generation recovery bundle into a recipient directory,
removes the sender's directories, checks read-only pending observations, retries
cleanup after a lost cancellation response, and repeats completed cleanup. SQL
assertions check consumer counts and the gateway's credential lookup semantics
before and after revocation. Completion requires absent sessions and namespaces
and verified cancellation or revocation without claiming test execution passed.
Frozen imported evidence must remain byte-identical and contain no credential.
This is HTTP/SQL portable recovery acceptance; it does not qualify VM execution
or demonstrate a deployment on a second physical host.

Restart acceptance uses real SIGKILL and verifies the process exit signal before
starting a new API process against the same database and schema. It keeps the
original account and bearer, without migrating or reseeding the child server.
The delivery proxy kills the API after observing committed consumer creation,
key creation and cancellation, before delivering the worker's successful response.
Fresh authenticated lookups must recover the original creation receipt and the
cancellation proof. A creation held before cancellation is released only after
the replacement API is ready, and must still conflict. A credential revoked
before the crash must remain unusable after restart. The recipient then repeats
cleanup with unchanged frozen evidence and verifies resource absence without
claiming test execution completed. The gate requires all three crash boundaries
for each ordering. PostgreSQL stays running; this does not test database crashes.
