# Campaign cancellation and cleanup recovery

Stop scheduling new batches while active native workers finish and clean up:

```sh
gregale test policy cancel --plan plan.json --directory campaign-current
```

The plan-bound request is durable. The scheduler polls while workers run and
checks before each new batch. Active batches are drained, complete receipts
remain evidence, and incomplete receipts remain uncertain. The checkpoint is
halted; resume through reconciliation and a fresh continuation rather than
removing the request or resetting checkpoint state. Interrupting the runner
itself still forwards its interrupt to native workers for their existing cleanup.

Once the runner is inactive, inspect a single attempted batch's workload sessions:

```sh
gregale test policy cleanup --plan plan.json --policy campaign.yaml \
  --manifest gregale-test.yaml --directory campaign-current --batch batch-id
```

Add `--execute` to explicitly retry deletion. Inspection is the default.
Imported campaigns require their active `--handoff bundle-index.json`.
Cleanup holds the runner and shared handoff locks. Sealed/reconciled or
transferred source campaigns cannot be cleaned through these commands.

Cleanup requires complete raw report arrays, configuration and revision-bound
terminal receipts, canonical native run IDs, and recorded app IDs for every
workload. It checks every live identity before any deletion, then rechecks each
identity immediately before deleting only that run's developer session.
Absent sessions are idempotent; lookup errors, replacements and unconfirmed
absence fail closed. The existing session API does not provide a conditional
delete by app ID; the final lookup and deletion are separate requests.
Missing/truncated evidence or missing workload identities require manual inspection.

Observations are saved separately as `cleanup-<timestamp>.json`; original
receipts and checkpoints are unchanged. This command does not rerun user fixture
commands, revoke consumers, remove scenario registrations, erase recorded cleanup
failures, grant coverage, or refund attempted budget. Run reconciliation with
`--check-cleanup` afterward. Recorded fixture/consumer cleanup errors remain
blocking even when workload sessions are absent. Cancellation is preserved by
the halted checkpoint during portable handoff; the request file itself is local.

Validation is deferred: the managed execution environment was unavailable when
this implementation was prepared.


## Live run identity journals

Campaign workers now create a private `batch-<id>.journal/` directory with a
plan/side/revision/batch binding and one atomic, synced JSON snapshot per native
run. The initial snapshot precedes provisioning; every API provisioning attempt
is marked `creating` before the request. Returned app IDs are saved before
further workload work. Native cleanup records `cleanup_started`, then
`deleted` or `cleanup_failed` for each workload. Secrets, tokens, response
bodies and assertion payloads are excluded.

When a worker dies before its final report is readable:

```sh
gregale test policy cleanup --plan plan.json --policy campaign.yaml \
  --manifest gregale-test.yaml --directory campaign-current \
  --batch batch-id --journal
```

Inspection remains the default. Add `--execute` for verified session deletion.
The directory must be inactive and owned, just as for receipt-based cleanup.
Journal mode is explicit and cannot bypass an already finalized checkpoint
report. Every snapshot must match the saved plan, side, revision, batch, scenario,
profile and workload projects. Returned IDs are checked against live sessions;
pending workloads are excluded. An unresolved `creating` state means the API
outcome is unknown and requires manual inspection. The unavoidable crash window
between remote creation and persisting its response cannot safely be guessed.

Cleanup observations include the journal snapshot digest; successful journal
cleanup updates its resource progress. Reconciliation reads available journals
for cleanup diagnostics when reports are missing or invalid, and
`--check-cleanup` performs read-only live checks. Journals never produce
assertion receipts, coverage credit, budget refunds or proof that a batch ended.
A missing execution report therefore remains blocking after resource cleanup.

Run journals can travel in portable campaign bundles as described below.
Existing campaigns without journals still use receipt recovery. Standalone
test invocations outside campaign workers do not create journals.


## Portable journal recovery

The normal `bundle export` command now includes journals for checkpointed
attempts in the root campaign and its recorded ancestry. No additional export
flag is needed. Each journal contains its exact binding and run snapshot bytes,
plus a portable inventory listing the expected snapshot names and checksums.
The bundle ID covers this inventory. Existing bundles without journals remain
supported; recipients of journal-bearing bundles need this updated Gregale.

Use `bundle inspect --from handoff.json` to see journal/run counts and the
number of uncertain provisioning intents. Import defaults to inspection-only.
For cleanup, activate the import using the existing shared claim directory:

```sh
gregale test policy bundle import --from handoff.json --directory recipient \
  --activate --claim-directory /shared/gregale-claims
gregale test policy cleanup --plan recipient/artifacts/plan.json \
  --policy recipient/artifacts/policy.yaml --manifest gregale-test.yaml \
  --directory recipient/work --batch batch-id --journal \
  --handoff recipient/bundle-index.json
```

Add `--execute` only to retry verified session deletion. Supply the original
bound manifest and platform authentication outside the bundle. The original
source machine is not consulted. The immutable journal bytes and inventories
stay under `artifacts/nodes/`; root snapshots are copied separately into
`work/`, where cleanup progress can advance. Re-exporting the work directory
captures the updated snapshots and transfers ownership again. Original
artifacts remain unchanged.

Import validates safe paths, exact inventory membership, all snapshot hashes,
run IDs, lifecycle states, binding equality, and checkpoint ancestry before
materializing anything or claiming authority. Root journals must match a saved
batch. Cleanup additionally checks workload membership against the supplied
manifest and checks returned app IDs against live sessions. Ancestor journals
remain immutable history rather than a second mutable cleanup owner.

Uncertain `creating` states remain uncertain after transport and cannot
authorize deletion. Missing execution reports remain blocking even after all
known resources are absent; journals grant no coverage, completion or budget
refunds. Uncheckpointed journals require local inspection before export.
Malformed/truncated snapshots or reports are rejected rather than silently
dropped. The existing 64 MiB decoded bundle, 4096-file, 33-node and 1024-run-per-
journal bounds apply. Shared claim directories must actually be shared by all
recipients; checksums protect integrity, not sender authenticity.


## Unified recovery assessment

Start with a read-only assessment to determine which recovery command applies:

```sh
gregale test policy recover --inspect --plan plan.json --policy campaign.yaml \
  --manifest gregale-test.yaml --directory campaign-current
```

Supply the exact original `--from` and `--baseline` inputs when the saved plan
requires them. Imported work also requires `--handoff recipient/bundle-index.json`.
Use `--recovery-bundle recovery/` to verify an existing reconciliation bundle
and obtain a command for inspecting its ready continuation. A matching blocked
seal can instead produce a reconciliation refresh command, using a new output
directory. The frozen directory is never reopened for cleanup.

The default assessment is offline. `--check-cleanup` authenticates only for
read-only session GET requests (30 seconds total, at most 1024 lookups). It
distinguishes recorded identities from absent, present, replaced and unknown
live resources. A deletion command is suggested only when the entire batch's
live identities match or are absent, at least one is present, and ownership,
locks and evidence permit recovery. The actual cleanup command rechecks these
conditions when invoked. Unknown provisioning outcomes remain manual work.

Output includes the existing verified campaign status, per-batch cleanup
evidence and its digest, resource identities, blockers, an overall recommendation,
and exact commands. Each command has an `argv` array, a quoted `posix_shell`
representation and a `mutates` flag. Nothing is executed. Cleanup inspection
itself saves observation files when later invoked, so its command is marked
as mutating even though it does not delete sessions. POSIX command strings are
for compatible shells; other consumers should use the argument arrays.

The suggested reconciliation directory defaults to the campaign directory plus
`-recovery`. Override it with `--output-directory`; assessment never creates
that directory. Reconciliation commands are conditional next steps after the
worker is inactive and cleanup is resolved; they do not promise that missing
test evidence or recorded cleanup errors can be repaired automatically.

Runner locks, shared claim locks, frozen/transferred sources, inspection-only
imports, conflicting evidence and invalid parent seals suppress cleanup advice.
Lock presence does not prove liveness; assessment never deletes stale locks.
Evidence, journal membership, checkpoint state and ownership are fingerprinted
around the read. Changes suppress all command suggestions and require rerunning
the assessment. This is an informational snapshot, not an execution gate or
a replacement for the checks performed by cleanup and reconciliation.

The command never acquires locks, deploys, runs fixture commands, deletes
resources or rewrites campaign evidence. Optional `--json assessment.json`
writes only the requested export outside protected campaign/import/recovery
directories, with input/output alias checks. Assessment returns zero for a valid
informational report even when recovery is blocked; invalid inputs or failed
inspection return one. Validation of this implementation remains deferred.
