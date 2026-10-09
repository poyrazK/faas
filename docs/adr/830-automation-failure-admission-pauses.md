# ADR-830: Automation failure admission pauses

- **Status:** accepted
- **Date:** 2026-10-10
- **Extends:** ADR-725 workflow alert signals, ADR-829 failure notifications,
  and dashboard automation publication and enabled intent
- **Decision:** Add an opt-in failure policy per published YAML or dashboard
  automation. Pause automatic admission when terminal failures meet the
  configured threshold and the window contains at least the configured
  minimum completed sample count. Count succeeded, failed and dead runs by
  terminal time; exclude cancellations, intermediate attempts and native
  Customer Operations custody. The window is bounded by monitoring start or
  the most recent explicit failure resume. Defaults are three failures,
  five completed samples and 300 seconds; count bounds are 1..10000 and
  observation windows are 60..86400 seconds.
- **Ownership:** apid writes policy intent. schedd evaluates a bounded,
  cursor-paged batch before its workflow schedule scan. Paused status lives
  in a separate operational guard rather than changing customer enabled
  intent. Evaluation and admission serialize on the existing app admission
  lock (or memory mutex). Detection is eventual across scheduler ticks and
  pages; already admitted runs continue. This policy is independent of the
  alert evaluator and does not require an alert rule to be enabled.
- **Enforcement:** Merge failure pauses into effective schedule/event trigger
  eligibility, including tenant schedules. Also check the guard inside
  captured event admission, so retained recipients cannot bypass a newer
  pause. Inbound webhooks record an ignored `automation_failure_paused`
  receipt. Manual runs and existing workflow wake/dispatch remain available.
  New events without an eligible recipient are not captured for later replay.
- **Persistence:** Policy edits, disabling monitoring, publishing, deleting
  and recreating a definition, and ordinary pause/resume actions do not clear
  the runtime latch. Keep one guard per app/name and a monotonically
  increasing transition generation. Policy writes use expected-version CAS.
  Configuration clones may carry the policy but reset runtime guards and
  transition history; the first evaluation initializes a fresh monitoring
  epoch if the guard is absent.
- **Recovery:** Provide an authenticated aggregate preview of pending,
  running and waiting runs plus retained, unadmitted event recipients. Resume
  requires the current pause generation and records the acting account.
  Start a fresh monitoring epoch, increment generation, and re-arm schedule
  cursors without catch-up for the paused interval. Resume never changes
  manual enabled intent. Counts are advisory and may change before resume;
  event retention and routing retry budgets still apply.
- **Notifications and history:** Atomically append immutable pause evidence
  and a recipient-snapshotted `automation.paused` app webhook outbox event
  with the latch transition. Concurrent evaluators produce one logical
  notification per transition. Reuse webhook delivery retry and idempotency;
  receivers must deduplicate deliveries. No configured recipient means no
  delivery. Explicit resume records generation, actor and aggregate evidence
  without an additional webhook. Expose the latest 100 transitions in state
  inspection. Include only owned app/name, transition generation, reason,
  policy revision, timestamps and counts, never run inputs, outputs, errors
  or tenant identities.
- **Rollout:** Apply the additive tables, effective-definition function and
  vocabulary migration, then update all admission/scheduler/API replicas
  before enabling policies. Older event admission workers do not enforce
  captured-recipient pauses. A downgrade must explicitly resume all failure
  pauses; the migration refuses rollback while any latch is active. Down
  removes policies, guards and guard history, but retains notification
  vocabulary for committed outbox events. No VM lifecycle changes exist.
