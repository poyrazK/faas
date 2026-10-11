# ADR-634 · The request-ID journal is asynchronous and never fails a request

- **Status:** proposed
- **Date:** 2026-10-07
- **Amends:** ADR-127 (production debugger). The exact public-ID index added
  in #3670 keeps its schema and RPC.
- **Decision:** gatewayd-internal no longer writes the request-ID journal before
  guest work.
  - **Queue.** `RecordRequestIDJournal` calls go through `RequestIDJournalQueue`:
    a 16,384-record buffer drained by 4 writers per gateway, each with a 2 s
    deadline.
  - **The request is always served.** The handler enqueues without blocking.
    A record the queue has no room for is dropped and counted as
    `gateway_request_id_journal_write_total{result="dropped"}`. A write that
    fails counts as `{result="failed"}`.
- **Why:** #3670 wrote the journal synchronously and answered
  `503 capacity_unavailable` ("Request correlation is temporarily unavailable")
  when the write failed, for every Hobby, Pro and Scale request. No ADR records
  that choice. On production-us (2026-10-07) a 100-VU closed-loop load on a warm
  Scale app hit it:
  - apid's 12-connection database pool was fully acquired, with about 22 s of
    acquire wait per second;
  - journal writes went from a p95 of 45 ms to the 2 s timeout;
  - 35% of requests answered 503, throughput fell from 106 to 50 rps and median
    latency rose to 2 s.

  The journal is a debugging index. It cannot decide whether a customer's
  request is served, and it should not be able to starve apid's pool for its
  API.
- **Consequences:**
  - Under sustained overload some request IDs are not resolvable through the
    exact index. Sampled request telemetry and traces still cover them.
  - A record is written a few milliseconds after its request instead of before
    it. A gateway crash loses queued records.
  - The journal holds at most 8 of apid's connections across the two compute
    gateways.
- **Rejected alternatives:**
  - Keep failing closed and raise apid's pool. That moves the ceiling, but the
    debugger index would still decide availability.
  - A batch RPC (one multi-row insert per flush). This is the right next step
    for throughput, but it changes the proto and apid. It is a follow-up; the
    queue is the seam it will plug into.
