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


## Recovery completion verification

After cleanup, explicitly recheck completion with read-only live lookups:

```sh
gregale test policy recover --inspect --verify-completion --check-cleanup \
  --plan plan.json --policy campaign.yaml --manifest gregale-test.yaml \
  --directory campaign-current --json recovery-summary.json
```

The `completion` section of the exported assessment is a shareable recovery
summary. It includes plan/checkpoint and evidence-snapshot digests, per-batch
resource identities and current live status, execution status, cleanup blockers,
and links to cleanup observations. The summary grants no ownership or execution
authority and must be rechecked when evidence or ownership changes.

Completion verification distinguishes:

- `resource_evidence_complete`: every attempted batch has a verifiable resource
  inventory, including all available journal identities. Uncertain provisioning,
  orphaned artifacts, malformed journals and identity conflicts keep this false.
- `known_sessions_absent`: fresh GET requests found all known workload sessions
  absent. This can be true while the full inventory is still unresolved.
- `test_execution_complete`: every batch on the selected revision side has
  completed or recovered execution evidence according to verified status.
- `recovery_complete`: all three conditions hold, campaign policy passes, the
  snapshot is stable, ownership is available, no runner/shared claim lock blocks
  verification, and no recorded cleanup errors remain.

Journal-only recovery cannot satisfy test execution completion. Pending batches,
missing reports and recorded fixture/consumer/workload cleanup errors remain
blocking after sessions are deleted. Session absence does not verify that a
fixture command, consumer revocation or namespace deregistration succeeded.

New cleanup observations use version two and bind plan, checkpoint, batch, side,
revision, evidence kind, resource identity digest and observation time. Journal
cleanup records both the pre-cleanup and resulting journal digests. Verification
automatically reads up to 256 local `cleanup-<timestamp>.json` files. To include
a separately transferred observation, repeat `--cleanup-observation PATH`;
this option requires `--verify-completion`. Observation files are not
automatically added to campaign bundles. An imported checkpoint can link an
external observation only if the original binding and current evidence still
match.

Observation links distinguish matching complete operations, partial operations,
legacy unbound records, stale evidence, unrelated checkpoints/batches, identity
mismatches and duplicate records. They provide provenance, not proof of current
absence. Version-one observations remain readable but cannot establish a bound
link. Repeated cleanup may change journal digests and make older observations
stale; this never erases the original files.

The verifier merges receipt identities with all journal identities, including
diagnostic runs omitted from primary assertion leaves. It performs at most 1024
additional GET requests within the assessment's existing 30-second deadline
(the assessment phase itself permits at most 1024). Lookup failures, replacement
apps and limit exhaustion never count as absence. Observation contents and their
directory membership join the before/after snapshot checks; changes invalidate
the completion claim and command suggestions. No locks are acquired or campaign
files changed. Optional JSON export is the only requested write.

A valid informational report still exits zero when recovery is incomplete.
Invalid options, inputs or failed inspection exit one. Implementation tests and
CI remain deferred.


## Portable end-to-end recovery acceptance suite

The `TestCampaignRecoveryAcceptance*` tests exercise the real campaign scheduler,
bundle import/export, ownership claims, cleanup CLI, recovery assessment,
completion verifier and reconciliation CLI together. An in-process HTTP server
implements only scoped session GET and DELETE; unexpected paths, workspaces,
authorization or mutating methods fail the test. No VM, deployment, lease renewal
or fixture command is needed.

The interrupted-worker case uses the native batch launcher injection point to
persist a provisioning intent and returned identity, then exits before publishing
the final report. It asserts an uncertain halted checkpoint, source freeze,
inspection-only and duplicate-recipient rejection, cleanup under the activated
recipient, immutable source snapshots, and bound version-two observation output.
A second handoff transfers cleanup progress and explicitly supplied observation
provenance. Fresh absence checks can then confirm resource cleanup while missing
test evidence still blocks completion and reconciliation.

Failure cases cover replacement app IDs, read failures, failed deletion and its
partial provenance, an unconfirmed provisioning outcome, a stale ownership claim,
and a recorded fixture cleanup error. A positive case supplies complete receipt fixtures
and recomputes status and policy through the CLI, then confirms that a
fully evidenced campaign with absent sessions can pass completion verification.
The suite checks that read-only assessment and reconciliation never issue DELETE.

Run the focused portable suite first:

```sh
go test ./cmd/gregale -run '^TestCampaignRecoveryAcceptance' -count=1
go test -race ./cmd/gregale -run '^TestCampaignRecoveryAcceptance' -count=1
```

After those pass, run the CLI package checks:

```sh
go test ./cmd/gregale
```

This suite tests integration contracts using an injected launcher failure; it
does not claim to exercise an actual OS process kill, native VM startup,
network faults on a deployed workload or real-VM cleanup. Those remain separate
native qualification targets.

The suite was added while this chat's managed execution environment was failed
and no shell runner was available. The commands above, compilation, formatting
and CI have not been run for this change; these are intended assertions rather
than a passing acceptance result.


## Reproducible portable validation gate

Run the same gate locally and in CI:

```sh
make test-gregale-recovery
```

It runs the gate's Python contract tests, verifies the Go toolchain against the
`go.mod` toolchain pin (currently Go 1.26.9), checks formatting for the entire
CLI package, compiles its test binary, and verifies discovery of the three
end-to-end acceptance tests. It then executes the focused recovery tests
uncached, first normally and then under the race detector. Required acceptance
tests must explicitly pass in both runs; missing tests, skips, malformed event
JSON, package failures and nonzero process exit codes fail the gate.

The focused filter includes campaign runner/recovery/bundle/journal/cancellation/
cleanup/status tests plus recovery assessment, completion, cleanup observation
and portable journal tests. Both runs compile the whole CLI package. This is a
portable recovery gate, not the full repository suite or native VM qualification.

The runner uses the pinned toolchain's `gofmt`, bounded CLI compiler concurrency,
one package build at a time, a default 6 GiB soft Go memory limit, and native
CGO-enabled race builds. Install Python 3 and the pinned Go toolchain plus a
working C compiler. Cross-compilation and disabled CGO are rejected rather than
silently skipping race verification. Tests use local HTTP fixtures; the gate
provides dummy CLI auth defaults and does not deploy workloads.

Evidence is written to a fresh directory under `.cache/gregale-recovery/`.
Override it with `RECOVERY_GATE_OUTPUT` or the script's `--output` option;
existing evidence directories are never overwritten. Evidence includes a JSON
summary with commands, exit codes, toolchain metadata, CLI/module source hashes,
per-stage logs, formatting diffs and raw normal/race test JSON. Binaries are
temporary and excluded from retained evidence. Formatting failures do not prevent
compilation/test diagnostics from being collected, but the final gate still
fails. Compilation or discovery failures prevent meaningless test runs.

The default gate never rewrites sources. To explicitly fix formatting and run
the checks, invoke:

```sh
python3 scripts/ci/gregale_recovery_gate.py --format
```

Review and commit the resulting source changes. The script never commits them.
A formatting failure cannot be waived by successful tests.

`.github/workflows/gregale-recovery.yml` runs this command for relevant pull
requests, main pushes, pushes to the current recovery feature branch, and manual
dispatch. It uses a read-only token and uploads evidence even on failure, with
14-day retention. Compiler/formatting diagnostics are also printed to job logs.
It neither opens nor merges a PR and does not replace the repository's existing
required checks.

The gate was authored without a usable shell in this chat. Its implementation
and the previously deferred recovery changes must not be described as validated
until this workflow or a local run actually passes.
