# Explain rate and deadline alert confirmation

The Go delivery-health watch returns local confirmation diagnostics in
`result.Confirmations`. Entries now cover all selected rate and deadline conditions:
backlog growth under throttling, the earliest-deadline signal, sampled modeled risk,
total upcoming/expired window counts, and reason-specific counts. Existing
receipt-budget diagnostics remain available with their existing semantics.

Join an entry to `result.Conditions` using its `metric`. The condition holds the
value, threshold, incident and configured sustain policy; the confirmation explains
how the current warning/recovery proof is progressing. These diagnostics do not
change the condition state, create notifications, mutate configuration or restore
proof from a checkpoint. No extra polling request or server endpoint is involved.

## Phases

| Phase | Meaning |
| --- | --- |
| `unknown` | No usable current proof; inspect `reason` and rate evidence |
| `clear` | The selected threshold is not breached and no incident is awaiting recovery |
| `baseline_pending` | Growth needs a comparable complete observation after its baseline, a gap or a policy change |
| `resume_pending` | A restored incident requires fresh warning or recovery confirmation |
| `warning_pending` | Eligible threshold breaches are accumulating fresh samples and sustain time |
| `firing` | The incident remains active; the recovery boundary is not currently confirmed |
| `recovery_pending` | Eligible below-boundary observations are accumulating recovery proof |
| `notification_blocked` | Warning proof is complete, but the durable outbox lacks opening capacity |

`fresh_samples`, `elapsed_seconds`, `required_seconds` and `remaining_seconds`
describe the **currently pending proof window**, not total polls or incident age.
Warning and recovery require two fresh, advancing eligible observations. The
remaining duration is computed at the last processed observation and does not
count down as wall time passes. More fresh evidence is still required. Outside a
pending window, zero duration/sample fields do not mean an alert is ready to open
or resolve; inspect the phase. The rate/deadline entries expose `required_samples: 2`
even while no proof window is active.

`observed_at` is present only when the condition has a current local metric
observation. A growth baseline alone supplies no growth measurement, so it does not
supply warning samples or a condition observation timestamp. An initial resumed
incident similarly does not present its checkpoint timestamp as fresh proof.

## Evidence failures and baselines

Condition-level clock, ordering and numeric failures take precedence over an
otherwise eligible aggregate read. Reasons include `stale_observation`,
`future_observation`, `repeated_observation`, `out_of_order_observation`,
`missing_observation`, `invalid_value` and `sample_gap` when applicable.

For unavailable reads or coverage, the diagnostic surfaces the corresponding
existing `RateEvidence` reason: health/summary read failure, binding mismatch,
incomplete coverage, invalid controls, unavailable window partitions, or preview
failure/truncation. Model, window and growth evidence are distinguished rather
than treating every unavailable condition as the same failure. Inspect
`RateEvidence` for the bounded supporting details. No raw event payload or error
message is added to confirmations.

Growth comparison waits use `growth_baseline_required`, `configuration_changed`
or `sample_gap` under `baseline_pending`. A resumed growth incident remains
`resume_pending` while the reason explains its missing comparison. Baseline waits
are not warning confirmation and never claim an incident resolved. The first
usable comparison can then start fresh warning or recovery confirmation.

`last_confirmation_loss_reason` and `last_confirmation_loss_at` describe the
condition's retained local loss diagnostics; their reason can remain the generic
`evidence_unavailable`. The current `reason` adds read-specific context. After
fresh evidence returns, a pending warning/recovery can coexist with that historical
loss. These fields are not a durable loss history and are not reconstructed on
resume. A reason with no recorded loss time is not presented as historical loss.

## Outbox pressure and resume

A warning that satisfies its duration and sample requirements can still wait for
notification capacity. `notification_blocked` with `alert_outbox_full` distinguishes
that case from waiting for warning sustain. Existing queue limits, reserved closing
slots, progress persistence and exact acknowledgements continue to apply. Diagnostic
snapshots describe the last collected metric state; acknowledging notifications
alone does not open an incident or provide a fresh metric observation.

Confirmation entries are local projections and are not added to checkpoint proof.
Resume still requires new evidence, identical selections and thresholds. Progress
callbacks retain the existing deep-copy isolation. Observing `clear` or recovery
for a deadline/reason count does not prove business completion: work can change
reason, age into expiry or leave retention. See
[deadline count and reason semantics](event-subscription-deadline-window.md).
