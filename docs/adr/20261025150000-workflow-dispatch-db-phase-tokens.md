# ADR 20261025150000 · Workflow dispatch slots give back their database token while a step runs

- **Status:** accepted (2026-10-25)
- **Date:** 2026-10-25
- **Amends:** [ADR-649](649-fair-bounded-workflow-dispatch.md) (four workflow
  execution slots per scheduler)

## Context

ADR-649 kept four workflow execution slots per scheduler and accepted that
"four different slow applications can occupy all four slots until they
complete, park or time out". A slot waits on the customer's step handler for
up to the plan's step timeout (10 minutes on Hobby, 2 hours on Scale), and
`api.WorkflowDispatchMaxPerApp` lets one application hold two slots. Two
applications with slow steps therefore stop every other customer's workflows
on that scheduler: sleeps wake late, events resume late, retries run late
(Hunt 13 H13-9).

The four slots also bounded the scheduler's database work. schedd's pool has
16 connections (`pkg/db.DaemonMaxConnections`) and the wake path shares it,
so simply raising the slot count would let workflow bookkeeping starve wakes.

## Decision

Separate the two bounds.

- **Slots wait on handlers.** `api.WorkflowDispatchSlots` rises to 32. The
  first `api.WorkflowDispatchBaseSlots` (4) are offered every tick as before.
  A further slot is offered only while every lower slot is still busy, so an
  idle scheduler makes the same claim checks as before and capacity grows by
  one slot a tick under load.
- **Tokens bound database work.** A goroutine running a dispatch slot holds
  one of `api.WorkflowDispatchDBPhaseSlots` (4) tokens while it reads or
  writes workflow state. It gives the token back while it waits on a step
  handler call (app steps, condition checks, code-first entry calls and
  integration calls) and takes one again for the database work that follows.
  `for_each` items each take their own token, and the parent gives its token
  back while it waits for them. No goroutine waits on another while it holds
  a token, so the tokens cannot deadlock.
- **Unchanged.** Claim fairness, the per-application (2) and per-tenant (1)
  in-flight caps, leases, generation fences and the five-second claim
  deadline stay as ADR-649 decided. Two slow applications now hold at most
  four of 32 slots.
- **Signal.** schedd exports `workflow_dispatch_slots_busy` and
  `workflow_dispatch_slots`; `FaasWorkflowDispatchSaturated` fires when every
  slot has been busy for 10 minutes.

## Consequences

- Sixteen applications with slow steps are needed to saturate a scheduler,
  instead of two.
- Workflow database concurrency per scheduler stays at four goroutines in
  dispatch slots. Untracked database reads remain: the once-a-second attempt
  checks of in-flight integration calls and item actions, and workflow work
  started outside dispatch slots (event delivery, cancellation), as before.
- A step's result may wait briefly for a token after its handler returns;
  holders keep tokens only for bounded database work.
- More step calls can be in flight at once (up to 32 per scheduler), so
  gatewayd-internal and integrations see more concurrent workflow calls under
  load. The per-application and per-tenant caps still bound any one customer.

## Alternatives

- **Raise the slot count only.** Simplest, but workflow database work would
  grow with the slot count and could starve the wake path.
- **Asynchronous steps.** Dispatching each step as a durable async invocation
  and resuming the run on completion would hold nothing while a step runs.
  It changes the run state machine and step result path; this decision does
  not preclude it.
