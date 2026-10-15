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
