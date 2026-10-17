# Operator force-park recovery

ADR-1525 applies to asynchronous `force_park` intents accepted by apid. Install
migration `20261017120000001` with operator dispatch quiesced, upgrade vmmd first,
then replace all schedulers together. Mixed scheduler versions are unsupported.
Running legacy parks become `outcome_unknown`; migration does not invent proof.

Poll the `status_url` returned by the acceptance response or use
`gregalectl instances wait --intent-id=<id>`. Polling exposes `park_execution`:

| Result | Meaning | Next step |
| --- | --- | --- |
| `cancelled` | Fresh traffic or active tail work won before preparation. VM remains running. | Review traffic before deliberately submitting another action. |
| `succeeded`, terminal `parked` | Original VM capture and resource cleanup were acknowledged. Terminal state, audit and durable publication handoff committed together. | Imaged publishes the eligible cache; a later wake may still cold-boot. |
| `succeeded`, terminal `stopped`, `cold_boot_required=true` | Original VM cleanup was acknowledged; capture was forbidden, failed, expired or became ineligible. Worker/job stops also use this result. | Next execution cold-boots. Inspect `reason` for capture failures. |
| `running` | A claim is preparing, executing or completing the durable plan. | Reclaim after the normal timeout resumes the original plan. |
| `outcome_unknown` | Execution started without a durable cleanup receipt, or original placement/lifecycle changed. | Inspect original node/wake/generation and native resource evidence. Do not blindly resubmit. |

Capture preparation binds the original physical VM generation, source start
stamp, migration lineage, unique capture keys and measured admission grant.
Reclaim changes the scheduler claim, never the physical effect token. Vmmd
records the token before acting, accepts only one first dispatch, and persists
capture/cleanup evidence independently of the scheduler claim. Duplicate RPCs
and a fresh vmmd process cannot repeat that operation.

After a durable late receipt, the queue can reclaim an uncertain intent only
when the original placement, wake, start stamp, mode and migration lineage still
match. Recovery performs database work only. A receipt for a prior VM cannot
stop a replacement or authorize another capture. A VM's absence by itself is
insufficient evidence. The ordinary watchdog leaves unresolved park ownership
for this protocol to reconcile.

A capture failure with confirmed cleanup is a stopped result. Failed cleanup
keeps ownership and admission reserved. Database or publication-handoff failure
rolls back terminal state, intent outcome, audit, host ports and barrier release;
retry completion against the existing receipt. A lost commit reply returns the
original completion, including its terminal state, without repeating effects or
creating another publication handoff. Measured capture acknowledgment publication
retains the existing native grant and input-freshness checks.

The poll response and audit omit claim/effect/migration/grant credentials. The
private execution journal retains native evidence for recovery. Downgrade refuses
to drop journal rows or unresolved legacy parks; preserve evidence before any
manual intervention. Legacy unary park and ordinary idle park do not implement
this operator recovery protocol.

Portable tests cover memory/PostgreSQL concurrency, fresh-traffic cancellation,
stale claims, failure rollback, publication handoff, lost replies, crash recovery,
late receipts, RPC authorization and generation-bound manager cleanup. Native
qualification remains required on supported x86_64 Linux KVM hosts: stage the
release images and helpers, run `make test-metal` (including
`TestMetalOperatorParkFailureAndGeneration`, `TestMetalOperatorParkCapture` and existing native snapshot/admission
and teardown suites), then `make leakcheck`. A skipped native test is not evidence
of physical cleanup or leak freedom.
