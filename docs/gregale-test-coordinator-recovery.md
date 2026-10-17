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

### Recovery during outages

Cleanup and fault completion have a two-minute operation budget. Each lookup or
mutation has a five-second request deadline. Transient read failures (timeouts,
connection failures, HTTP 408/429/500/502/503/504 and truncated responses) receive
at most three attempts, with 250 ms then 500 ms backoff. Cancellation and the
operation deadline stop further attempts. Numeric and HTTP-date `Retry-After`
values are respected; a requested delay above the two-second local wait limit
leaves the lookup unresolved for a later invocation instead of retrying early.
Authentication, ownership conflicts, malformed proofs and unsupported capabilities
are not retried.

Mutations are attempted once. A failed cancellation, revocation or deletion may
already have committed, so cleanup stops and the next invocation observes the
current durable proof before deciding whether another mutation is needed. Resume
with the same cleanup command after connectivity returns. Recovery never creates
a consumer or key to resolve an outage.

Failure observations retain every journaled consumer, registration and session.
Unvisited resources and failed observations remain `unverified`; they cannot
prove absence or completion. Issues record the resource, operation, stable reason,
lookup attempt count and whether a later attempt may succeed. Raw server error
bodies and credential values are excluded. Observations live in the recipient's
work directory; frozen journals and imported artifacts remain unchanged.

The PostgreSQL gate also cuts the API fixture's real SQL connections through a
local TCP proxy, independently of the parent SQL oracle, and tests an HTTP API
outage. Both must exhaust the read budget without mutations, retain unresolved
inventory, reject completion and finish safely after connectivity is restored.
This tests connectivity loss while PostgreSQL remains running.

### Opt-in cleanup watcher

For a local interrupted campaign with its original process-aware `runner.lock`:

```sh
gregale test policy cleanup-watch \
  --plan campaign-plan.json --policy campaign-policy.yaml \
  --manifest gregale-test.yaml --directory /private/campaign \
  --execute --grace 1m --interval 30s --attempts 120
```

For an activated portable import, also supply its `--handoff` index. The watcher
never discovers arbitrary directories: it monitors only this explicit target.
It requires the coordinator and every recorded worker to be verifiably terminated
with unchanged ownership evidence throughout the grace period. Elapsed resource
expiry is not authority. Gregale currently has process ownership proofs rather
than a renewable server owner lease.

The worker invokes `recover-lock --execute`, then the existing
`cleanup --journal --faults --fault-history --execute` path. That path reacquires
claim and runner exclusion, verifies the entire inventory and recovery lineage,
and persists cleanup observations separately from immutable execution evidence.
API/database outages retain unverified resources; later attempts inspect durable
outcomes before issuing further mutations. Each cleanup invocation retains the
existing two-minute operation budget. Attempts include ownership polls; defaults
are 120 attempts at 30-second intervals, plus time spent in cleanup operations.

Standard output contains JSON assessments, cleanup observations, and watch-state
records (`waiting_for_owner`, `owner_terminated_grace_period`,
`cleanup_unresolved`, `cleanup_complete`, `manual_inspection_required`). Exit 0
means the cleanup operation completed; exit 2 means it did not. This does not
certify test execution or refund attempted budget. Redirect output to a protected
operator log if watch-state history is required. Existing cleanup observations
remain in the private work directory.

The private work directory now contains `cleanup-watch-receipt.json`, written
and synced after original runner recovery and before cleanup can mutate resources.
It binds the exact plan/policy/manifest paths and contents, checkpoint, full
resource inventory, activated handoff index, API endpoint hash, and original
process identity. Returned cleanup observations are referenced by name and hash;
cached progress never substitutes for fresh API verification. No bearer or
consumer key is stored in the receipt.

Restart the same command with the same inputs to resume. A Linux kernel guard
excludes a second watcher for the command's lifetime. The replacement verifies
all prior receipt owners have terminated in the same boot/PID namespace, repeats
the grace period, and durably records its successor identity before recovering
stale exclusion. Only cleanup/recover-runner locks owned by receipt identities
in this exact directory and plan qualify. Any workers attached to those watcher
locks, foreign claim targets, active owners, changed checkpoints/journals/progress,
and transferred or changed handoffs stop automatic recovery. History is bounded
to 32 watcher identities; exceeding it requires manual inspection.

Original owner recovery interrupted before the first receipt was synced still
requires explicit recovery. Missing or legacy receipts never authorize takeover.
Unverifiable remote-host or reboot identities also require manual inspection.
Ctrl-C stops between operations; an already running bounded cleanup operation
finishes first. Attempt limits apply to each watcher invocation; cumulative
cleanup attempts are retained in the receipt.

### Cleanup watcher acceptance

`make test-gregale-recovery-postgres` requires
`TestScenarioConsumerPostgresCleanupWatch` and all ten cases: API and database
outages, restart between attempts, restart in flight, repeated restart, missing
receipt, changed progress, changed checkpoint, changed handoff, and concurrency.
The fixture uses the authenticated production API subprocess and PostgreSQL,
with separate coordinator, tracked worker, and watcher processes. It kills the
coordinator first, verifies the orphan worker still blocks cleanup, then kills
the worker and checks the stable termination grace period.

Transport outages must persist three generations of unverified fault/session
inventory while the SQL credential oracle still sees the original key as usable.
Restoring connectivity lets the surviving watcher finish, including recovery
from a lost cancellation response. Killing the watcher between attempts or
after a cancellation commits but before its response arrives must now permit
verified automatic takeover from the durable receipt. Repeated watcher deaths
must preserve every prior owner identity. Missing or changed evidence must stop
the replacement without resource mutations; a concurrent watcher cannot displace
the active owner. Restoring the original test evidence allows normal recovery.

Every case checks revoked credentials, absent sessions and registrations,
creation fencing, unchanged frozen bundle evidence, and no repeated fixture or
key provisioning. Resource cleanup does not certify missing execution receipts.
This is CLI/API/PostgreSQL recovery acceptance; it does not qualify VM lifecycle
or native fault injection.

### Watcher status in JSON and offline HTML

Use the existing command with the same target inputs:

```sh
gregale test policy status \
  --plan campaign-plan.json --policy campaign-policy.yaml \
  --manifest gregale-test.yaml --directory /private/campaign \
  --json /private/reports/status.json --html /private/reports/status.html
```

Include the original `--handoff` index for a portable import. Exports must remain
outside campaign and recovery directories. Default status does not call the API, acquire
or recover locks, create a watcher guard, or rewrite receipts and journals.
Linux guard inspection reads the kernel lock table without taking a lock.

The additive `cleanup_watcher` JSON object and HTML panel show:

- Receipt owner liveness and whether the kernel guard is held, including during
  takeover before the replacement has updated the receipt.
- Cumulative cleanup attempts, previous owner count, and the last bound cleanup
  observation with consumer, registration and session states and retry diagnostics.
- Local evidence verification, resume binding verification, blockers, and both
  structured command arguments and a quoted command for the next action.
- `active`, `ready_to_start`, `ready_to_resume`, `cleanup_recorded_complete`, or
  `manual_inspection_required` states. A planned campaign with no watcher is
  `not_started`; an original runner still requiring inspection is `waiting_for_run`.

Saved cleanup observations and completion are historical. They do not prove live
resource absence and never change verified execution counts, coverage or the
policy verdict. The configured original API profile is needed to verify resume
binding; without it, local evidence and recorded progress remain inspectable,
but readiness stays unverified. No authentication request is made. Generated
watcher commands use the current default grace and retry settings; explicit
operator flags can adjust those within the supported bounds.

Readiness is advisory: the actual watcher repeats every ownership, evidence and
exclusion check before acting. Changes to receipts, progress, inventory or guard
ownership during inspection mark the snapshot unstable and remove resume
readiness. Invalid primary plan, policy, manifest or handoff inputs retain the
status command's existing validation errors.

Recovery acceptance requires watcher status tests and also checks the public
command during real API/database outages, with a stopped guard owner, after
watcher death and after cleanup completion. The fixture verifies zero additional
API requests, unchanged campaign files and no invented test execution evidence.

### Opt-in live resource verification

Add `--verify-live` to `test policy status` with `--directory` and the original
plan, policy, manifest and handoff inputs. Default status remains offline. Live
status issues only authenticated GETs for the full verified journal inventory:
consumers, registrations and isolated developer sessions. Session lookup does not
renew a lease. No cleanup, key provisioning, lock recovery or observation-file
persistence occurs; only explicitly requested status exports are written.

The `live_verification` JSON object and HTML table show each resource's current
observed state, check time, successful verification time, previous saved state,
change classification and sanitized failure diagnostics. Only successfully
verified reads receive `verified_at`. Unreachable, unauthorized, unsupported or
identity-mismatched resources stay `unverified`; cached absence never supplies a
live result. Resources unvisited after the deadline retain their inventory rows
without a verification timestamp. Confirmed session deletion compares as absence,
rather than reporting false drift against a later absent session.

Reads share a 30-second operation budget and the recovery read policy: five-second
requests, at most three attempts, bounded backoff and Retry-After handling. The
report retains independent results when one resource cannot be verified. A
watcher receipt, when present, must match the configured API destination and
bound inputs before any network request. Changed local evidence during the
checks invalidates live verification and resume readiness.

`all_verified` means every inventory resource was verified; `resources_clean`
means those observations found absent sessions/registrations and absent, revoked
or durably cancelled consumers. A pending creation intent is not clean. These
are per-resource point-in-time reads, not an atomic server snapshot or an
execution/cleanup gate. They cannot certify missing test receipts, coverage or
policy success. Exit 0 means the informational report was produced; inspect the
live result rather than treating the exit code as cleanup proof.

Acceptance checks default status makes zero API requests, opted-in status makes
GETs without mutations, API/database outages retain uncertainty, completed
cleanup verifies live timestamps, and campaign/receipt bytes remain unchanged.

### CI cleanup gate

Use `test policy status --verify-live --require-clean` with the original
`--plan`, `--policy`, `--manifest`, and `--directory` (and `--handoff` when needed).
JSON stdout and optional `--json` / `--html` exports are written before the gate
returns, with a concise summary on stderr. Gate exit codes are:

- 0: every expected resource was verified clean.
- 4: verification completed, but resources remain present or consumers pending.
- 5: verification is incomplete, blocked, empty, or local evidence changed.

Invalid inputs and export failures retain ordinary CLI error codes. The flag
requires `--verify-live`; default informational status behavior is unchanged.
A clean resource result does not establish execution completeness or policy
success: CI must check those separately. Verification remains point-in-time.

### Wait for cleanup in CI

Add `--wait-clean --timeout 5m --interval 10s` to `test policy status` with the
original plan, policy, manifest, directory and any handoff. `--wait-clean` implies
`--verify-live --require-clean`. Each attempt performs fresh read-only checks;
it never starts cleanup or mutates resources. Run the cleanup watcher separately.

The overall timeout includes API reads and delays. Timeout is bounded to 1s–1h,
interval to 1s–1m and no greater than timeout; these duration flags require
`--wait-clean`. Each individual attempt retains the 30s live-verification budget.
Polling stops on the first clean observation, blocked verification, changed
immutable evidence, cancellation, or deadline. Transient read failures can retry.
Watcher progress may advance between attempts; changes during an individual
snapshot still invalidate that observation. Original inputs and the full resource
inventory remain pinned across attempts, as does the observed API endpoint.

Only the final report is emitted to stdout and optional JSON/HTML exports. Its
`clean_wait` field includes attempts, elapsed milliseconds and the stop outcome.
At deadline, exit 4 means the final attempt verified resources remain non-clean;
exit 5 means verification is incomplete (also used for cancellation). Exit 0 requires a clean observation
before the deadline. Execution and policy gates remain separate.

### Cleanup CI workflow acceptance

The PostgreSQL recovery gate requires `TestScenarioConsumerPostgresWaitClean`
and all six cases: present-to-clean, API outage recovery, database outage
recovery, watcher restart, deadline, and changed evidence. Separate processes run
the public watcher and status commands through the production API and PostgreSQL.
The same portable three-generation fixture checks durable cancellation, the real
credential SQL oracle, resource removal, no repeated provisioning, and immutable
parent evidence. These tests require no KVM and do not qualify VM fault behavior.

While the watcher is stopped between attempts, status must make fresh reads
without mutations or evidence writes. Successful cases then allow the watcher to
finish while the status process continues polling. Failure cases verify exit 4
for resources still present at deadline and exit 5 for changed immutable evidence;
both retain JSON and HTML diagnostics without awarding execution coverage.
The gate rejects missing or skipped cases and now requires 27 PostgreSQL test
results, including top-level suite results.

The database-outage case also covers exhaustion of a single live-verification
attempt's 30-second budget followed by recovery within a 60-second overall
wait. Only the fixture's deliberately parked creation request receives a longer
client timeout; production read budgets and the status command are unchanged.

### Combined execution, policy and cleanup gate

Use `gregale test policy verify` with the original `--plan`, `--policy`,
`--manifest`, and `--directory`, plus any original `--from`, `--baseline`,
`--handoff`, or `--recovery-bundle` inputs. It shares status inspection and export
protections, always enables live verification, and performs no execution or
cleanup mutations. Optional `--wait-clean --timeout 5m --interval 10s` waits only
for cleanup; it cannot adopt new test receipts while waiting.

The report's `ci_verdict` contains separate execution, policy and cleanup states
(`passed`, `failed`, or `incomplete`), reasons, the checkpoint-side scope, expected
and verified batch counts, and the overall result. Execution requires every
batch in that scope to have complete verified passing receipts (including full
verified recovery adoption). Failed tests or recorded cleanup failures remain
failures even after live resources disappear. Policy is reevaluated from the
bound reports, including required coverage and environment evidence. Cleanup
requires fresh verification of every expected resource using the existing gate.

Exit 0 requires all three checks to pass; exit 6 means the combined verdict did
not pass. Invalid options, unreadable initial inputs and export errors retain
ordinary CLI error codes. JSON stdout and optional `--json` / `--html` exports
are written before returning the verdict. This does not change default `status`
or its cleanup-only gates. Explicitly disabling live/cleanup flags cannot bypass
verification in the combined command.

Execution and policy input files are pinned before inspection and checked through
the final verification, including batch receipts, checkpoint/seal, original
reports, recovery adoption files, and parent lineage proofs. Changed or unreadable
evidence yields an incomplete result. Watcher progress may advance as documented
for `--wait-clean`; successful cleanup never changes the execution verdict.
