# ADR-1521 · The request-ID journal is written in batches

- **Status:** accepted
- **Date:** 2026-10-15
- **Amends:** ADR-634 (asynchronous request-ID journal). Takes the "batch RPC"
  alternative ADR-634 named as the next step.
- **Decision:** gatewayd-internal writes the request-ID journal to apid in
  batches, and apid stores each batch with one statement.
  - **Gateway.** `RequestIDJournalQueue` keeps its 16,384-record buffer and
    4 writers. A batcher now takes the first queued record, gathers more for
    up to 100 ms or 128 records, and hands the batch to a free writer. While
    every writer is busy, records keep queueing and the next batch fills at
    once.
  - **RPC.** `RecordRequestIDJournalBatch` carries 1..512 records. apid reads
    each distinct account once, skips records whose plan keeps no debugger
    data, and inserts the rest with one `INSERT … SELECT FROM unnest(…)`. The
    statement keeps the single insert's rules per row: the app must belong to
    the account, and a repeated record id is accepted only with identical
    content. A malformed record, a repeated id within the batch, or an unknown
    account is counted as rejected instead of failing the records around it.
    A storage failure fails the whole call.
  - **Mixed versions.** When apid answers `Unimplemented`, the gateway writes
    one record per RPC, as before, and retries the batch RPC after 5 minutes.
    Compute nodes may roll out before the control plane's apid.
  - **Metrics.** `gateway_request_id_journal_write_total{result}` still counts
    records, now with a `rejected` value for records apid refused.
    `gateway_request_id_journal_write_duration_seconds` is observed once per
    batch. `FaasRequestIDJournalWriteFailures` fires when failed records are
    both above 1 per minute and above 5% of records, because one failed batch
    now counts up to 128 failures.
- **Why:** the journal kept the control-plane database on every customer
  request. On production-us (Oct 14, 2026) the journal held exactly one row
  per request: 226 for 226 requests, 250 for 250, and 2,892 for the 2,891
  successful requests of a spike. Each row cost one apid RPC, one account read
  and one insert on the 2-vCPU control plane, which reached 96% CPU at about
  157 req/s while the compute nodes stayed at 19–27%. Batching makes apid's
  work grow with flushes per gateway (at most about 10 per second per writer)
  instead of requests. This is the first item of Stage 1 in the scaling
  roadmap: take the control plane off the request path.
- **Consequences:**
  - A record reaches apid up to 100 ms later than before, plus the batch's
    write time. The journal is a debugging index, so this is invisible to
    customers.
  - A gateway crash or restart loses up to one batch per writer in addition
    to the queue ADR-634 already allowed to lose.
  - A storage failure now fails up to 128 records at once.
  - The single-record RPC stays for the mixed-version window and can be
    removed once every apid serves the batch RPC.
- **Rejected alternatives:**
  - `COPY` into the journal: it cannot express the ownership join or the
    idempotent-retry rule without a staging table per call.
  - Writing the journal from the gateway straight to Postgres: gatewayd-internal
    has no database credentials for customer tables, and apid is the owner of
    this table.
  - Sampling the journal: the debugger promises that every request ID of a
    debugger-enabled plan resolves.
