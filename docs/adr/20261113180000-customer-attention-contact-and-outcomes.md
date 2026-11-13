# ADR-20261113180000: Customer attention signals, round 2 — more kinds, contact and outcomes

Status: Accepted

Extends ADR-20261113123000.

## Context

The first attention signals cover failed deployments, refusal bursts and
stuck custom domains. Customers also get stuck in ways those rules miss: an
app that keeps running out of memory or failing to boot, a scheduled job
that fails every night, a plan limit they keep hitting, a payment that
failed. And a signal on its own changes nothing: staff had no way to reach
out from it, and no record of whether signals were worth acting on.

## Decision

### Five more kinds

| Kind | Opens when | Source |
| --- | --- | --- |
| `app_oom` | ≥ `AttentionOOMKills` (3) runtime OOM kills of one app within `AttentionOOMWindow` (24 h) | `instances.workload_oom_failed` events (schedd emits one per kill, live release or not) |
| `boot_failing` | ≥ `AttentionBootFailures` (5) failed boots of one app within `AttentionBootWindow` (1 h) | `wake.boot_failed` events; detail is the newest reason |
| `jobs_failing` | the newest `AttentionJobFailures` (3) finished scheduled runs of an active job all failed or dead-lettered within `AttentionJobWindow` (7 d) | `job_runs` with `trigger_kind = scheduled`; a cancelled run breaks the streak |
| `payment_failing` | a payment failure within `AttentionPaymentWindow` (14 d) with no recovery since | billing payment failure and recovery receipts |
| `limit_pressure` | refusals of one plan-limit or quota code on ≥ `AttentionLimitDays` (3) UTC days within `AttentionLimitWindow` (7 d) | recorded customer API errors with codes `plan_limit_*`, `*_quota_exceeded` or `*_limit_exceeded`; rate limits are excluded |

HTTP crons record no outcome, so they are not covered; command jobs are.
`boot_failing` includes platform causes on purpose: the customer is affected
either way, and `FaasCustomerAttentionSpike` separates a platform-wide cause.

### Contacting a customer

From the staff page, **Contact customer** opens a form prefilled with a
draft for the signal's kind (subject, category, message naming only what the
customer can see in their own dashboard). Sending it calls
`support.Service.ContactCustomer`, which opens a ticket in the customer's
personal support scope:

- the first message is the staff member's, and the sender is the assignee;
- the first response counts as given at creation and the ticket waits for
  the customer (`pending`), so no service clock runs until the customer
  replies; a reply resumes the clocks and starts the next-reply target;
- the customer is notified like any staff reply (`staff_reply`), and the
  ticket shows in their support inbox;
- the write is idempotent (form-held key) and needs the usual staff write
  gates (CSRF, same origin, session principal, five-minute step-up).

apid then links the ticket to the signal: the signal is dismissed with
outcome `contacted` and its `ticket_id`. A resubmitted form replays the
ticket and links it again, so a failure between the two steps heals on
retry. Signals of an organization-owned app still reach the account that
owns the app; organization scopes are out of scope for v2.

This is the first customer-visible part of attention signals, and it is a
human decision each time: nothing is sent automatically.

### Outcomes

`customer_attention_signals.outcome` records what support did:

- `contacted` (with `ticket_id`, enforced by a CHECK);
- a dismiss outcome staff must pick: `platform_issue`, `already_in_touch`,
  `not_needed`, `false_positive` (the free-text reason becomes an optional
  note);
- `resolved_on_own`, set by the evaluator when an open signal clears with no
  action.

The staff page summarises the last `AttentionSummaryWindow` (30 days) per
kind: signals, open, contacted, other dismissals, false positives (with
their share of acted-on signals), resolved on own, and the median time from
first seen to action. `apid_customer_attention_outcomes_total{kind,outcome}`
counts the same. These are what thresholds get tuned against.

### Compatibility

The migration widens the kind CHECK (new name, NOT VALID, validated, old one
dropped) and adds `outcome` and `ticket_id` bare with NOT VALID CHECKs
validated after. While the previous apid release still evaluates, it
resolves signals of the new kinds (it does not detect them); the new
release reopens them on its next pass. That churn lasts one rollout and
leaves `resolved_on_own` rows that the summary counts.

## Consequences

- Staff can reach a stuck customer in one step from the signal, and every
  signal ends with a recorded outcome.
- False-positive rates per kind make threshold changes evidence-based.
- Support tickets gain a staff-initiated flavour (`staff_initiated` in the
  ticket state); reports count them like other tickets.
- Automated customer-facing hints remain a separate decision.
