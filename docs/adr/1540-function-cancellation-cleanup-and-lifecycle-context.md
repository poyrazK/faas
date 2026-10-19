# ADR-1540 · Function cancellation completion and trusted lifecycle context

- **Status:** accepted
- **Date:** 2026-10-16
- **Decision:** A shared interpreter slot remains occupied until its abandoned
  invocation acknowledges completed cleanup, or its worker is retired. Node
  handlers receive `ctx.signal`; Fetch handlers also receive a following
  `request.signal`. Python async handlers receive task cancellation, and sync
  handlers may poll `ctx.cancelled`, a `threading.Event`. Acknowledgement follows
  handler and response-stream completion, including async `finally` blocks.
  The existing five-second acknowledgement deadline retires an uncooperative
  worker and fails the other requests sharing it. Recycle counts cancellation
  cleanup as active work (ADR-687). This completes ADR-1342's slot accounting;
  it adds no plan limits or automatic invocation retries.
- **Why:** Previously the pool released a slot as soon as the HTTP reader left.
  Node suppressed output without notifying the handler, and Python acknowledged
  task cancellation before cleanup finished. A database request could still be
  running when the runner admitted replacement work or recycled its worker.
- **Consequences:** Pass the Node signal to `fetch` or
  the PostgreSQL starter adapter's `query`. Python sync cancellation is cooperative;
  blocking code is bounded by worker retirement. Arbitrary detached work is
  outside this completion fence. In particular this change does not qualify
  the generated adapter's `waitUntil` scaffold as durable post-response work.
  A cancellation cannot undo an already committed mutation, and an uncertain
  COMMIT still needs application idempotency before any retry.
- **Lifecycle context:** The optional envelope field `lifecycle_hook` becomes
  `ctx.lifecycle_hook` in Node and Python. Only POST from the actual socket
  peer `127.0.0.1`, `::1` or its IPv4-mapped equivalent with one matching
  guest-init marker (`X-Faas-Before-Checkpoint: 1` or `X-Faas-After-Restore: 1`)
  sets `before_checkpoint` or `after_restore`. Both markers, duplicates and
  malformed values fail closed. Request bodies and forwarded client addresses
  cannot set this context. Ordinary invocations receive an empty string.
  Handlers must also match their configured hook path. Hooks run in one
  interpreter process: this is not a broadcast or a VM-wide pool-drain proof.
  ADR-351's capture failure policy and the restore recycle barrier are unchanged.
- **Rejected alternatives:** Immediate acknowledgement and slot reuse permit
  overlap with unfinished cleanup. Always killing the worker interrupts healthy
  neighbours. Trusting only an event header allows public clients to forge a
  lifecycle call. Full post-response tail accounting and broadcasting resource
  hooks across multiple worker processes need separate implementation and
  native acceptance.

## Evidence

Production adapter subprocess tests hold Node and Python cleanup behind a gate:
a neighbour finishes before cancellation is acknowledged; acknowledgement arrives
only after release, and cancelled frames never escape. A Fetch stream test checks
both signal propagation and reader cancellation. Worker-pool tests exhaust all
slots before a delayed acknowledgement and verify replacement work cannot enter.

The opt-in `TestConcurrentFunctionPostgres` uses the production HTTP runner,
generated adapter and shipped SDK runtime against real TLS/SCRAM SQL with a
restricted customer role. Independent SQL witnesses check blocked-write rollback,
bounded local admission, neighbour continuity, checkpoint/restore callbacks,
lost idle SQL sessions, worker death, context isolation and no mutation replay.
These are portable process tests, not snapshot or frozen-thaw qualification.
See [qualification procedure](../ops/function-backend-concurrency-acceptance.md).
