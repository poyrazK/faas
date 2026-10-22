# ADR-20261021210000 · Enrolled apps receive no account events

- **Status:** accepted
- **Date:** 2026-10-21
- **Amends:** [ADR-20261019180000](20261019180000-rehearsal-side-effects-stay-inside-the-run.md), [ADR-20261020090000](20261020090000-rehearsal-event-capture.md)
- **Decision:** An app with a `scenario_test_action_policies` row is never a recipient of the account's event fan-out. Its event subscriptions and workflow event triggers are left out of every recipient snapshot.
- **Why:**
  - **Leaks in.** ADR-20261019180000 deferred the recipient side of event fan-out. A production event published while a rehearsal ran was delivered to the enrolled preview apps. Candidate code then ran on real production data.
  - **Nondeterminism.** The extra deliveries made the evidence depend on unrelated production traffic. One run could receive an event its sibling never saw.

## Decision

- **Trigger.** `enqueue_event_fanout()` is replaced. It is otherwise unchanged, but now skips three kinds of recipient whose app has a rehearsal policy:
  - event subscriptions;
  - workflow event recipients (`workflow_event_recipients`);
  - tenant workflow event recipients (`workflow_tenant_event_recipients`).
- **MemStore.** The memory store applies the same rule when it snapshots recipients.
- **Scope.** The exclusion follows the policy row, not the run's state, as the Phase 0 exclusions do. An enrolled app stays excluded after its run seals.

A rehearsal exercises event-driven behaviour from its recipe. The recipe sends the event to the app's path, or publishes it from a run member: such publishes are captured as run evidence (ADR-20261020090000) and never fanned out.

## Consequences

- **Cost.** Fan-out gains one primary-key lookup on `scenario_test_action_policies` per matching subscription or workflow trigger. Recipient snapshots for non-enrolled apps are unchanged.
- **Snapshot timing.** Events already in `event_fanout_outbox` before enrollment keep their recipient snapshot. A rehearsal app is enrolled before it is deployed, so those snapshots cannot name it.
- **Not covered.** These paths still include enrolled apps, and are left to their own owners:
  - **Event preview.** `POST /v1/events:preview`, through `ListMatchingEventWorkflows`, lists matching workflows. It has no side effects.
  - **Explicit recipients.** Object-storage notifications and webhook automations carry explicit recipients for one app. A rehearsal app has neither binding, because managed bindings are refused at enrollment.

## Rejected alternatives

- **Filtering at delivery time in schedd.** The recipient snapshot is the durable record of who an event was for. Dropping recipients later would leave work that is never delivered and never accounted for.
- **Changing the recipient functions themselves.** That would also change the event preview, and widen the replacement from one function to three.
