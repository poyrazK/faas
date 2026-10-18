# Data repair health and recovery

Applies to `FaasDataRepairStalled`, `FaasDataRepairNodeUnavailable`,
`FaasDataRepairOutcomeUnknown`, and `FaasDataRepairHealthStale`.

## Inspect the current state

Open the operator-only `/dashboard/admin` repair section, or request
`GET /v1/admin/ops/data-repairs` with an admin-scoped credential, the operator
allowlist, and the existing MFA requirements. The response is uncached and uses
one primary-database snapshot. `limit` and `cursor` page the detail rows; counts,
oldest phase ages and retained capacity always cover the entire active set.

Record the operation/plan ID, task ID, original node, phase age, node status,
cancellation flag and failure code. Captured values, SQL, snapshots, executor
credentials and native producer/lease frames are absent. Check the observation
time before acting. An unavailable read returns an error, rather than an empty
healthy queue. The dashboard displays a warning if its read fails.

If only the original bad import ID is known, use
`gregale data-repair history SOURCE_UUID --app APP --environment ENV --customer CUSTOMER_UUID`
or **Find repair history** on the review page. Follow the recorded parent and
replacement IDs/digests, explicit source closure and committed receipt.
History contains metadata and never retries or resolves an apply. A missing
receipt alone does not prove rollback; preserve the original attempted repair
UUID for the existing resolution protocol.

Source history survives payload compaction and normal hosted ledger expiry.
After hosted history expires, use read-only
`data-repair get PLAN_UUID --app APP --source SOURCE_UUID` and
`data-repair verify PLAN_UUID --app APP --source SOURCE_UUID --digest PLAN_SHA256`,
adding exact environment/customer selectors when needed. These reads do not
recreate an Operation, restore approval authority or settle a hosted outcome.
Journal purge makes current verification explicitly unavailable while preserving
the committed result. See [history discovery](../data-repair.md#find-every-repair-attempt).

For source setup failures, use
`gregale data-repair doctor --app APP --environment ENV --customer CUSTOMER_UUID --table public.products`
or **Executor readiness** on the review page. Missing upgrades, runtime grants,
capture/version triggers and schema drift have explicit corrective actions.
Unavailable executors remain unknown. Doctor changes no rows or plans and
cannot settle an uncertain apply; preserve its original repair UUID and follow
the resolution protocol. See [readiness](../data-repair.md#inspect-repair-readiness).

For application capture failures, use the SDK's capture readiness helper on the
actual writer pool, or run local
`gregale data-repair doctor --profile capture --database-env APP_DATABASE_URL --scope-file repair-scope.json --table public.products --release RELEASE`.
This profile checks writer grants, the selected base receipt inbox and session
settings without executor privileges. Column-only UPDATE does not authorize the
enrollment lock; use the owner's reviewed table-level UPDATE grant. A ready
executor report does not establish writer readiness. Neither profile can recover
updates made outside capture or while triggers were bypassed. See
[writer diagnostics](../data-repair.md#inspect-an-application-writer).

## Follow the phase

| Phase or condition | Next step |
|---|---|
| `queued` | Check the exact executor binding, schedd dispatch gate, pinned runner availability and account/node capacity. Use the existing rehearsal cancellation action if the work is no longer needed. |
| `running` | Inspect the original node/task and worker. Cancellation joins the existing attempt and its cleanup; it does not permit another VM with the same task ID. |
| `cleanup` | Restore contact to the original node. Inspect its vmmd repair task and native ownership journals, then allow schedd's existing retirement retry to finish. Keep capacity reserved until physical retirement is confirmed. |
| `publishing` | Physical retirement is already recorded. Check the original executor binding, saved plan digest and evidence publication. Reconciliation reads or publishes the saved evidence; do not rerun the guest. |
| `applying` or `outcome_unknown` | Inspect the original executor plan and saved repair operation receipt. The existing repair status/reconciliation flow reads the receipt. To stop retrying and prepare a replacement, explicitly resolve the apply outcome; only a matching receipt or durable source closure settles it. Any explicit retry must reuse the same plan digest and repair operation ID. Never generate a new repair UUID to bypass an unknown result. |
| `resolving` | Resume `data-repair resolve` with the original Operation ID, digest and exact binding. A source commit returns its receipt; otherwise resolution permanently closes the old plan under the apply lock. A busy or unavailable source stays unresolved. A lost closing response may already have committed closure: repeat the same identity. Only confirmed closure permits a fresh replacement. Normal retention preserves the reservation and events. |
| `replacing` | Read the original Operation's `replacement_operation_id`. Resume `data-repair replace` with that same UUID, original parent digest and exact executor binding. A lost response may already have committed the source child and parent fence. Do not choose another replacement UUID or apply the parent. Normal retention preserves this pending reservation and its events. |
| Unavailable, missing, retired or stale-heartbeat node | Restore the original node's access and journal history. A changed daemon incarnation can retire an enrolled original attempt. Missing journals after node replacement remain unconfirmed and retain capacity. |
| Health observation stale | Check schedd and its primary database connectivity. Restore the health reader before treating existing gauges as current evidence. |

Neither elapsed time, cancellation, a healthy heartbeat nor the operator view
authorizes journal deletion or reservation release. Do not edit task phases,
erase native records, run generic Operation recovery for a repair, or restore the
entire source database to clear these alerts. These views add no mutation API.

## Metrics and alert timing

Schedd samples independently of the rehearsal worker every 30 seconds with a
10-second query budget, including when new rehearsal dispatch is disabled.
`schedd_data_repair_phase_count` and
`schedd_data_repair_oldest_phase_timestamp_seconds` use eight fixed phase labels:
`queued`, `running`, `cleanup`, `publishing`, `applying`, `outcome_unknown`, `replacing`, `resolving`.
Task, operation, account, application and node IDs never become metric labels.

`schedd_data_repair_capacity_held` reports slots, admission memory including VM
overhead, vCPUs and CPU millicores reserved by running/cleanup tasks. It is a
durable reservation projection, not measured host usage. Publishing and unknown
apply outcomes, resolution and replacement preparation do not reserve a
rehearsal VM.

Phase entry time survives cancellation and retry updates. Legacy records without
that field conservatively use creation time. Warning ages come from
`pkg/api/limits.go`: queue 120 seconds, running 150 seconds, cleanup/publishing
60 seconds, and applying/replacing/resolving 120 seconds. Stalled and
unknown-outcome conditions must persist for a minute; unavailable-node and stale-observer conditions for
two minutes. Unknown apply outcomes require attention regardless of age.

Gauges retain their last successful snapshot after a failed read.
`schedd_data_repair_health_last_success_timestamp_seconds` and
`schedd_data_repair_health_collection_total{outcome="error"}` expose that gap.
Repair-state alerts exclude observations older than 120 seconds; the separate
health-stale alert covers the missing evidence. Replica observations use `max`
rather than adding the same fleet snapshot more than once.

## Verify recovery

Refresh the operator view. Confirm that the original task reached a terminal
phase, or that saved evidence finished publication and the repair now awaits
approval. For apply uncertainty, confirm the original executor receipt and its
projection into the Operation, or a source-confirmed `closed` outcome before preparing a replacement. Check that the health observation is fresh and
that retained capacity fell only after retirement proof. Preserve audit receipts
and unresolved history according to the retention policy.

This monitoring does not qualify native execution or change the internal-preview
boundary. The designated-host repair gate and serving-daemon/node failure
qualification remain required before rollout; see [repair qualification](../data-repair.md#qualification).
