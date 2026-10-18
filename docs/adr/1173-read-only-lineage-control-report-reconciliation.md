# ADR-1173: Read-only lineage control report reconciliation

Status: Accepted
Date: 2026-10-13
Related: ADR-1170, ADR-1171, ADR-1172

## Decision

Add subscription-lineage-retry-control-plan-reconcile APP SUB with required --file
PLAN, --source-report PREVIOUS, and --report NEW. Strictly validate the unchanged
normalized plan and matching bounded source report before authentication or server
reads. Reject a mismatched subscription locally and resolve the target app to the
saved stable UUID. Create a new private, exclusive, synced execution journal before
inspection; preserve the original plan and source report. No controls are sent.

Inspect every fixed command, including entries not recorded before interruption,
using existing job and complete guarded receipt GETs. Reuse preflight's scope,
incarnation, retention, progress and journal verification. A matching action receipt
becomes acknowledged with its immutable metadata. Stored local acknowledgments are
never blindly trusted. Reconciliation does not generate new UUIDs or reselect jobs.

Add optional evidence to execution results. receipt_present means a verified
matching command receipt; receipt_absent means a complete observed journal did not
contain that UUID; command_conflict means the UUID is recorded for another action.
expired, stale_guard and unavailable describe missing usable evidence. All outcomes
except a matching receipt remain unconfirmed. A missing receipt is not proof that
a control never ran, and ready/no_op/terminal/capacity states do not become control
acknowledgments. Job and journal reads remain separate observations.

Write one synced result per plan entry in a fresh existing-format journal, bounded
by its existing 1 MiB limit. This output is usable by apply --resume-report or another
reconciliation; resume continues to recheck receipts before considering a POST.
Older readers that reject the new optional evidence field require upgrading.
Stop further reads after a persistence failure, emit the full in-memory summary
with remaining unavailable entries, and exit unsuccessfully. Do not claim entries
were saved when persistence failed.

The stdout summary includes read_only, complete, recorded_entries, acknowledged,
unresolved, results, source/output paths and any persistence error. Complete means
all entries were assessed and results persisted; definite absence/conflicts or
expired/stale guards can be complete without being resolved. Failed reads, scope
errors, interruption or persistence errors make it incomplete. Exit 0 only when
all commands have verified acknowledgments and persistence succeeds, 1 for any
unresolved entry or persistence failure, and 130 on interruption after partial
reporting. Independent read failures do not prevent inspecting other entries.

Apply existing per-read and total deadlines. No server routes, SDK operations,
schema, retention policies, quotas or compliance registries change. Local reports
are editable evidence, not authenticated audit records or retention reservations.
