# ADR-1120 · AI gateway throughput: pipelined admission and settlement, keyed settlement retries

- **Status:** accepted
- **Date:** 2026-10-14
- **Relates to:** ADR-998 (preview rollout), ADR-991 (hard caps), ADR-988 (AI gateway)
- **Decision:**
  - **Admission in three pipelined round trips.** The Postgres backend runs one transaction with three pipelined batches:
    1. the policy reads: the clock, the account, the binding and the integration;
    2. the locked reads: create the state row on first use, lock it, update the binding's usage counters, delete expired leases while totalling live ones, and read the end user's day;
    3. the writes: state, lease and binding counters.

    The order of lock acquisition does not change. The admission-state row is locked from the second batch to the commit.
  - **Admission reports the breaker and the provider cooldown.** It already reads both under the row lock, so `Decision` carries:
    - `CircuitClosed` when the configured breaker is closed;
    - `ProviderGate` when the stored cooldown belongs to the call's policy revision.

    The handler then skips `AllowCircuit` and `AllowProviderRequest`. A half-open breaker or a new policy revision still takes the separate check, which owns the probe claim and the cooldown reset.
  - **A breaker outcome that changes nothing takes no lock.** Two outcomes are common:
    - a success with no failure streak;
    - any outcome while another call holds the breaker open.

    These are recognized from an unlocked read. Skipping them places the outcome at that read, which is a valid order for concurrent calls. Probe outcomes and failures still take the row lock.
  - **Settlement in two pipelined round trips, keyed by the lease.** Settlement first claims the call's lease ID in `outbound_ai_settlements`, reads the limits and inserts the request-log row. It then updates the contended rows in one batch:
    - the caller/model rollup;
    - the end-user rollup;
    - the admission counters;
    - the binding counter.

    In the same batch it deletes the lease and stores the captured content. A settlement whose key is already taken changes nothing.
  - **Settlements are retried.** The handler makes up to `OutboundAISettlementAttempts` (3) attempts of `OutboundAISettlementAttemptTimeoutSeconds` (5 s) each, about 100 ms apart. It retries only a keyed record and never an invalid one. Each retry counts `outbound_ai_usage_record_events_total{event="retried"}`, and `state_error` now means that every attempt failed. outboundd's hourly prune deletes keys older than `OutboundAISettlementKeyRetentionMinutes` (60).
  - **The resolver reads in one round trip.** It pipelines three reads:
    - the integration;
    - its attachments and bindings;
    - its current price list.
- **Why:**
  - The preview soak (ADR-998) put every call of one integration behind its admission-state row. On that row:
    - admission held the lock across about nine sequential round trips;
    - the breaker check and outcome took it again in their own transactions;
    - settlement held it, and the caller/model rollup row, across the request log, the alerts and the content capture.
  - A call cost 30.3 round trips, or 39.2 with a breaker.
  - Production outboundd has two pooled connections, and one is parked for LISTEN. Every call of a process therefore shares one query connection, so the total round trips per call bound throughput as much as the lock does.
  - ADR-998 left settlement unretried, because a timed-out commit may still have landed. It named idempotent settlement keyed by the lease as the follow-up. A key makes the retry safe, so a slow commit no longer loses usage and leaks the caps.
  - Deleting the lease inside the settlement moves the call's reservation into the settled counters atomically. Concurrent admissions no longer see the call twice, once settled and once reserved, between settlement and release.
- **Consequences:**
  - **Fewer round trips.** A call takes 11 round trips, or 12 with a breaker. `TestAIGatewayRoundTripBudget` fails if a change adds one. Measured soak results are in the runbook (`docs/runbooks/FaasAIGateway.md`).
  - **Shorter lock hold.** Admission holds the row for two round trips and the commit; settlement for one and the commit.
  - **Release becomes a no-op for settled calls.** The handler still releases every lease, so a backend or test double that settles without deleting the lease cannot leak it. Each settled call therefore spends one round trip on a delete that finds nothing.
  - **Extra state lock for some settlements.** A settlement with no tokens and no cost now locks the state row before it deletes its lease, so the delete follows admission's lock order: state, then lease.
  - **A new operational table.** `outbound_ai_settlements` holds about two hours of keys, one row per settled call.
  - **Memory backend parity.** The memory backend reports the same `Decision` fields and keys its settlements, so handler tests exercise the same paths.
- **Rejected alternatives:**
  - **Raise outboundd's pool.** `pkg/db` sizes daemon pools against the fleet's connection budget and requires production pool metrics before changing one. Fewer round trips per call help at any pool size.
  - **Admission as one PL/pgSQL function.** One round trip, but the token bucket, the hard caps and the per-minute windows would be written twice: once in Go for the memory backend and once in SQL. Keeping the two in step would be a constant risk.
  - **`synchronous_commit = off` for admission.** It would release the row lock before the WAL flush, but a Postgres crash would lose committed leases and counters. The hard caps would then under-count exactly when the system is recovering.
  - **Retry without a key.** A retry after a commit that timed out but landed would count the call twice.
