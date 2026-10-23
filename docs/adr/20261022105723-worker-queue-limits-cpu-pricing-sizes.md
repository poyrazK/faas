# ADR-20261022105723 · Worker plan decisions: queue backlog and retention, CPU pricing, worker sizes

- **Status:** accepted and implemented. Decisions 2 and 3 change nothing.
- **Date:** 2026-10-22
- **Builds on:** ADR-1140 (worker pull leases), ADR-152 (configurable app
  CPU), ADR-1430 (plan entitlements from the October 2026 COGS report),
  spec §4.7 (billing on plan RAM + 8 MB).
- **Context:** The worker track left three plan decisions to the owner:
  queue backlog and retention limits per plan, CPU pricing, and worker sizes.
  - **Queues today** (Free / Hobby / Pro / Scale):
    - pending messages per app across all its queues (`MaxQueueDepth`):
      0 / 5 / 25 / 100;
    - message size: none / 64 KB / 256 KB / 1 MiB;
    - attempts before dead letter: 0 / 3 / 10 / 25.
    - A pending message never expires.
    - Finished results are kept 1 / 7 / 30 / 90 days.
    - One worker lease takes up to 100 messages (ADR-1140), yet a Pro app
      refuses its 26th pending message.
  - **CPU today:** billing counts GB-RAM-hours only. Sampled CPU is
    telemetry (ADR-039). Every app may use up to 1000 millicores whatever
    its RAM. ADR-152 left shapes above 1000 millicores to a plan-cap and
    pricing decision.
  - **CPU cost estimate:** on the bare-metal candidate in the 2026-10-16 OVH
    survey (24 threads, 96 GB, €99.99 a month) a thread-hour costs about
    €0.0057 and a GB-hour about €0.0014.
  - **Margin of a fully busy CPU** against the $0.01 overage (ADR-1430
    counts $ as €):
    - 1 CPU per 2 GB: about 57%;
    - 1 CPU per 1 GB: about 29%;
    - 1 CPU per 256 MB, which a 256 MB app may run today: a loss.
- **Decision:**
  1. *Queue backlog and retention (worker plan step 4).* Free still has no
     queues.
     - **Pending messages per app** (`MaxQueueDepth`): Hobby 10,000, Pro
       100,000 and Scale 1,000,000.
     - **Pending queue bytes per account:** Hobby 100 MiB, Pro 1 GiB and
       Scale 10 GiB (ADR-1430's binary billing units). This is new; it
       bounds what a backlog costs the control-plane database.
     - **Expiry:** a pending queue message older than 1, 4 or 14 days, by
       plan, moves to the dead letter, measured from its enqueue. This is
       new. A message a worker holds under a lease does not expire while
       leased.
     - Message size and attempts are unchanged.
  2. *CPU pricing (step 8).* CPU stays bundled into the RAM price: there is
     no CPU meter, and billing stays GB-RAM-hours (spec §4.7). A shape above
     1000 millicores, if one is ever added, gets CPU in proportion to its
     RAM, at most 1 CPU per 2 GB, so the GB-RAM-hour price keeps covering
     its CPU.
  3. *Worker sizes (step 9).* Workers keep the plan caps: RAM up to the
     plan's app RAM, CPU of 250, 500 or 1000 millicores. There are no
     larger worker sizes.
- **Implementation of decision 1:**
  - *Counters, not counts.* A capped enqueue used to lock the app row and
    count the app's pending work: cheap at 100 rows, O(backlog) per send at
    1,000,000. An account byte cap cannot sum payloads on every send either.
    - `invocation_backlog_counters` holds pending counts per account, app
      and source, and payload bytes for queue rows.
    - Each key is split over eight slots by invocation id, so concurrent
      writers rarely share a counter row.
    - Row triggers on `invocations` keep the counters exact. A row joins
      the counts on entering pending or dispatching and leaves them on
      leaving both states or on deletion; a payload rewrite adjusts its
      bytes.
    - A claim moves a row from pending to dispatching, which changes no
      count; the triggers' `WHEN` keeps claims off the counters, so the
      drain does not contend with producers.
    - Bytes are the payload's length as text, measured the same way on
      every change.
    - The migration creates the triggers and counts the existing pending
      rows in one transaction. The triggers' lock keeps writers out until
      it commits, so no row is missed or counted twice.
  - *Admission.* The counters include environment-owned rows, so their
    sum bounds the app's production backlog from above.
    - Below the cap, admission admits without counting.
    - At the cap, it counts the production backlog as before, under the
      app lock it already holds.
    - The byte cap sums the account's queue counters. Apps of one account
      share no lock, so two concurrent sends to different apps may pass
      it by their own sizes.
  - *Expiry.* A partial index covers pending queue rows by `created_at`,
    built with `CREATE INDEX CONCURRENTLY`.
    - The 60 s invocation retention sweep runs up to ten batches of 1,000.
      Each batch moves expired messages to `dead_letter`, with outcome code
      `queue_message_expired`, before the sweep stamps their result
      retention.
    - A message a worker holds is dispatching, so it is not touched.
  - *Errors.* The count cap keeps `plan_queue_depth`. The byte cap returns
    `403 plan_queue_backlog_bytes` with the limit and the observed bytes.
  - *Values* live in `pkg/api/limits.go` (`MaxQueueDepth`,
    `MaxQueueBacklogBytesPerAccount`, `QueueMessageRetentionSeconds`) and
    in `docs/event-driven.md`.
- **Consequences:**
  - A queue can absorb a real burst.
  - A backlog with no consumer stops growing at the byte cap and drains
    itself to the dead letter.
  - The exposure in decision 2 remains: a CPU-saturated 256 MB worker costs
    more than its overage revenue. It is bounded by the worker replica caps
    (1 / 3 / 10 per account) and by the plan's included hours. Revisit it
    if worker CPU telemetry shows such workloads.
  - Workers needing more than 2 GB or 1 CPU are not served.
- **Rejected alternatives:**
  - *Raising only the message count* (×100) leaves backlog size unbounded
    by bytes and lets messages wait forever.
  - *Metering CPU separately* makes bills vary with load. It contradicts
    the predictable billing of spec §4.7, and it needs meterd, provider,
    ledger and financial-model changes.
  - *Larger worker sizes* (2 GB / 1 CPU up to 8 GB / 4 CPU on Pro and
    Scale) are deferred. When added, decision 2's ratio applies.
