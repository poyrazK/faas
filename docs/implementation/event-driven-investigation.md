# Event driven reliability findings

Findings investigated against commit `46aad2a12` on 2026-11-11. Repairs are
implemented locally; see [the remediation status](event-driven-remediation.md)
for current verification and rollout requirements. The status, code references
and reproduction results below describe the original baseline. P1 findings
were proposed release blockers; P2 findings were correctness or operational
issues. Release qualification remains separate from runtime repairs.

| ID | Priority | Baseline finding |
|---|---|---|
| ED001 | P1 | NATS consumers expire during idle scheduling gaps |
| ED002 | P1 | Kafka retains completed payloads behind a poison offset |
| ED003 | P2 | The NATS pool wrapper hides retry scheduling |
| ED004 | P2 | Redis leaves pending work with retired consumers |
| ED005 | P1 | Data API change events bypass company routing isolation |
| ED006 | P2 | Schema registration breaks retries of retained accepted events |
| ED007 | P2 | Retention health reveals company event identities after access removal |
| ED008 | P2 | Registered schemas stop Data API change hooks |
| ED009 | P1 | External broker dead-letter replay reports success without executing |
| ED010 | P2 | Accepted event identities can make every HTTP delivery fail |
| ED011 | P2 | Permanent routing errors are retried after a subscription matches |
| ED012 | P2 | Circuit probes time out despite successful claim renewal |
| ED013 | P1 | NATS token credentials leak through retained consumer errors |
| EDG001 | Release gate | Reference-host qualification and launch items remain incomplete |

Deploy the ED005 and ED013 repairs before promotion, including the operational
follow-up in the remediation document. Keep the original evidence here for
traceability.

## Confirmed runtime defects

### ED001 NATS consumers expire during idle scheduling gaps

- **Priority:** P1. **Status:** Open, reproduced against NATS 2.11.17.
- **Code:** `pkg/sched/poller_nats.go:253` and
  `pkg/sched/dispatch_triggers.go:2545`.
- **Trigger:** A trigger acknowledges its batch, then waits more than the
  broker's inactive threshold before its next scheduler lane runs.
- **Defect:** `natsConsumerConfig` supplies `Name` but no `Durable` field.
  JetStream creates an ephemeral consumer with a five-second inactive
  threshold. The cached poller only rebuilds when the connection closes,
  not when its consumer disappears.
- **Evidence:** The real broker deleted the consumer after 5.208 seconds
  without another pull; the next poll returned `nats: no responders available
  for request`. A separate eight-second idle probe returned consumer-not-found.
  Pending unacknowledged messages extended the lifetime, so a slow handler alone
  did not reproduce this failure.
- **Suggested fix:** Set `Durable` and recover a missing consumer without
  requiring a scheduler restart or trigger configuration change.

### ED002 Kafka retains completed payloads behind a poison offset

- **Priority:** P1. **Status:** Open, reproduced with the production poller
  and a scripted Kafka reader.
- **Code:** `pkg/sched/poller_kafka.go:398`, `:546`, and `:562`.
- **Trigger:** A poison record uses `broker_poison_strategy: seek-to-offset`
  while later messages continue arriving in its partition.
- **Defect:** Polling continues past the open offset, while the settled queue
  retains every completed `kafka.Message`, including its payload. Neither
  retained bytes nor reading ahead is bounded. One trigger can eventually
  exhaust the scheduler's memory.
- **Evidence:** One poison offset retained 2,000 completed 8 KiB payloads
  (16,384,000 bytes, 15.625 MiB), with zero commits. Retention grew with input.
- **Suggested fix:** Bound reading ahead and retain compact commit progress
  rather than completed message bodies.

### ED003 The NATS pool wrapper hides retry scheduling

- **Priority:** P2. **Status:** Open, reproduced through the production wrapper.
- **Code:** `pkg/sched/nats_broker_pool.go:157` and
  `pkg/sched/broker_trigger_delivery.go:183`.
- **Defect:** `pooledNATSPoller` embeds the narrow `triggerSource` interface
  without forwarding `Defer` or `NackUntil`. Dispatch cannot detect the wrapped
  poller's retry scheduling support and falls back to ordinary `Nack`.
- **Evidence:** A one-minute retry used a two-second broker redelivery delay.
  The existing NATS retry test constructs the bare poller and misses this path.
- **Impact:** Cooling retries repeatedly consume broker, database and polling
  capacity instead of remaining at the broker until due.
- **Suggested fix:** Forward both optional interfaces through the pool wrapper.

### ED004 Redis leaves pending work with retired consumers

- **Priority:** P2. **Status:** Open, reproduced with miniredis.
- **Code:** `pkg/sched/poller_redis_streams.go:128`, `:173`, and `:190`.
- **Trigger:** Recreate a trigger with a new ID while keeping its Redis stream
  and consumer group, after the old trigger received an unacknowledged message.
- **Defect:** The new trigger gets a new consumer name. Poll reads only that
  consumer's pending list and never-delivered entries. No group-wide idle
  reclaim path adopts work assigned to the retired consumer.
- **Evidence:** After advancing broker time by one day, the replacement got
  zero records and the message remained pending under `old-trigger`.
  An ordinary restart retaining the trigger ID is not this reproduction.
- **Suggested fix:** Add bounded idle-message reclaim with ownership fencing.

### ED005 Data API change events bypass company routing isolation

- **Priority:** P1. **Status:** Open, reproduced with MemStore and PostgreSQL.
- **Code:** `pkg/apid/appevents/app_producer_publish.go:64` and
  `pkg/apid/dataapievents/handler.go:134`.
- **Trigger:** A Data API app continues publishing after its creator loses
  developer access to its company; the creator has a matching personal app
  subscription under the same account.
- **Defect:** `PublishAppProducerEvent` never stamps
  `envelope.Producer = eventcontract.AppProducer(appID)`. Missing producer
  metadata deliberately uses legacy unrestricted fanout, bypassing
  `event_recipients_in_producer_reach`'s company boundary.
- **Evidence:** With company membership removed, a valid `api.orders.inserted`
  publication persisted `producer=""` and captured the creator's personal app
  as a recipient of the company's row-change payload. Both stores reproduced it.
- **Suggested fix:** Stamp the authenticated app producer before acceptance;
  qualify routing and replay reach after removal and role downgrade.

### ED006 Schema registration breaks retries of retained accepted events

- **Priority:** P2. **Status:** Open, reproduced with MemStore and PostgreSQL.
- **Code:** `pkg/apid/appevents/events_publish.go:49` and
  `pkg/apid/appevents/events_publish_batch.go:84`.
- **Trigger:** An event is accepted without a registered schema, its producer
  loses the acceptance response, and a schema is registered before it retries.
- **Defect:** Single and batch account publication validate against current
  schema requirements before checking the retained event identity. The
  app-publication endpoint already performs retained-identity lookup first.
- **Evidence:** Retrying identical source, ID, type and data changed a successful
  202 into 422 (`event schema version is required`). Batch publication reported
  the retained duplicate as permanently rejected (`retryable:false`).
- **Impact:** A producer cannot confirm an accepted publication after schema
  introduction without changing its identity or content, risking duplicate
  side effects if it publishes a replacement.
- **Suggested fix:** Recognize a matching retained acceptance before applying
  current schema admission rules; preserve conflict detection for changed data.

### ED007 Retention health reveals company event identities after access removal

- **Priority:** P2. **Status:** Open, reproduced with MemStore and PostgreSQL.
- **Code:** `pkg/apid/appevents/retention.go:61` and
  `pkg/state/event_retention_queries.sql:3`.
- **Trigger:** An app creator leaves a company whose retained event rows remain
  under the creator's account, then requests account-wide retention health.
- **Defect:** `/v1/events/retention` returns event-level samples without the
  `eventAccess` producer/company checks used by receipt endpoints. The optional
  app check only runs when the caller supplies an app filter.
- **Evidence:** The same removed creator got 404 for the company's event receipt,
  but 200 retention output containing its source, event ID, acceptance time,
  retained-byte count and retention deadline. Both stores reproduced it.
- **Suggested fix:** Apply company visibility to sample rows before returning
  them, including account-wide and source-filtered requests. Account-level
  aggregate counters may retain their documented semantics.

### ED008 Registered schemas stop Data API change hooks

- **Priority:** P2. **Status:** Open compatibility defect, reproduced with
  MemStore and PostgreSQL.
- **Code:** `pkg/api/data_api_change_events.go:19`,
  `pkg/apid/appevents/app_producer_publish.go:25` and `:64`, and
  `cmd/gregale/templates/data-api/changes.mjs:53`.
- **Trigger:** Register a schema for a Data API app's own source and a generated
  row-change type such as `api.orders.inserted`.
- **Defect:** The registry requires an explicit schema version whenever a
  schema exists. Neither the runtime change-event DTO nor `AppProducerEvent`
  can carry one, and the publisher always constructs an envelope without one.
  The relay cannot publish matching events even when their data satisfies
  every registered schema.
- **Evidence:** A change published before registration. Registering `v1` with
  an accepting `{"type":"object"}` schema made the next valid change
  permanently rejected (`retryable:false`, `event schema version is required`).
  Both storage implementations reproduced it.
- **Impact:** The relay marks such changes failed instead of publishing them.
  The documented retry-after-fixing-the-cause flow cannot supply a version;
  the current public API also has no schema-removal operation.
- **Suggested fix:** Define a version-selection contract for change hooks and
  carry the selected version through the recorded change, relay DTO and
  publisher. Preserve the selected version across retries and do not silently
  bypass registered schema validation.

### ED009 External broker dead-letter replay reports success without executing

- **Priority:** P1. **Status:** Open, reproduced with MemStore and PostgreSQL,
  the production Kafka poller, and a scripted Kafka reader.
- **Code:** `pkg/state/queue_replay.go:52`, `pkg/state/queries.sql:13617`,
  `pkg/state/pgstore.go:32672`, and `pkg/sched/dispatch_triggers.go:517`, `:826`.
- **Trigger:** An external broker record reaches `max_attempts`, its broker
  delivery is settled, and an operator retries its receipt or replays its
  unified dead-letter entry.
- **Defect:** Both APIs only reset the stored trigger record to `pending`.
  They create no durable replay work and do not republish it to the broker.
  The scheduler returns on an empty broker poll and only claims records
  whose identities appear in that poll. A committed Kafka message no longer
  supplies that delivery; NATS termination and Redis acknowledgment have the
  same dependency on unavailable original deliveries.
- **Evidence:** A `max_attempts` record committed Kafka offset 7. Both direct
  record retry and unified DLQ replay then returned success, but three further
  scheduler dispatches invoked no handler and left the record `pending`.
  Unified replay also stamped `ReplayedAt`, despite executing no work. All four
  combinations of API and storage implementation reproduced the failure.
  Kafka settlement used a scripted reader, not a live Kafka cluster; NATS and
  Redis replay were traced in code, not separately executed for this finding.
- **Impact:** The documented recovery action silently strands recoverable
  events after the original broker message has been settled. The unified
  entry is marked replayed, hiding the failed recovery attempt.
- **Suggested fix:** Durably schedule a fenced replay from the retained
  payload without requiring the original broker delivery. Define how this
  work settles independently of the original broker offset, and explicitly
  reject records whose payload was intentionally omitted.

### ED010 Accepted event identities can make every HTTP delivery fail

- **Priority:** P2. **Status:** Open. Ingress reproduced with MemStore and
  PostgreSQL; routing and bridge transport reproduced locally.
- **Code:** `pkg/eventcontract/cloud.go:156`, `:170`,
  `pkg/state/event_routing_admission.go:161`,
  `pkg/sched/event_fanout.go:347`, and `pkg/vmmdgrpc/forward.go:1736`.
- **Trigger:** Publish an event with an internal newline in its ID or type,
  such as the JSON string `"order\ncreated"`.
- **Defect:** Ingress checks these fields for presence and length but accepts
  HTTP-invalid characters. Routing copies them verbatim into
  `x-gregale-event-id` and `x-gregale-event-type`. The HTTP/2 bridge transport
  rejects those header values before they can reach an application.
- **Evidence:** Both public ingress tests returned 202 and persisted a receipt
  for each unsafe field, in both stores. A separate test used real event
  routing admission to create the invocation, then the production bridge
  request builder and HTTP/2 transport with an in-process server. Valid
  identity control delivered successfully; newline IDs and types failed with
  `http2: invalid HTTP header value` and zero handler requests.
- **Impact:** Accepted events cannot reach any matching HTTP consumer.
  Retries and replay preserve the same invalid identity and cannot repair it.
- **Suggested fix:** Define a reversible HTTP binding for identity values, or
  reject unsupported characters consistently before acceptance. Keep the
  envelope identity stable; do not silently strip or rewrite identity bytes.

### ED011 Permanent routing errors are retried after a subscription matches

- **Priority:** P2. **Status:** Open, reproduced with the real scheduler
  fanout sweep in MemStore and PostgreSQL.
- **Code:** `pkg/sched/event_routing_retry.go:48` and
  `pkg/state/event_routing_admission.go:240`.
- **Trigger:** An event matches a keyed subscription, but its retained payload
  cannot resolve the subscription's work key, for example `data.order_id`.
- **Defect:** Admission correctly classifies the missing key as
  `invalid_subscription`, `retryable:false`. The retry decision only stops a
  permanent error when `matched` is false, or when it wraps `ErrNotFound`.
  A deterministic failure discovered after matching is scheduled again.
- **Evidence:** A real fanout sweep persisted `state:pending`, `attempts:1`,
  `retryable:false` and a next-attempt time for a missing `order_id`, rather
  than terminal progress with `retry_stop_reason:non_retryable`. Both stores
  reproduced the result. The existing invalid-filter test fails before
  matching, so it does not cover this case.
- **Impact:** Immutable invalid inputs repeatedly consume routing capacity and
  may count as repeated failures toward opening the consumer's circuit.
  Operators see a non-retryable error that nevertheless remains scheduled
  until its attempt or duration budget is exhausted.
- **Suggested fix:** Honor permanent error classification after matching as
  well as before matching, while preserving explicit retryable storage errors.
  Cover missing work keys and fairness keys through full routing admission.

### ED012 Circuit probes time out despite successful claim renewal

- **Priority:** P2. **Status:** Open, reproduced in MemStore and PostgreSQL
  using controlled clock inputs and real claim/renewal methods.
- **Code:** `pkg/state/event_circuit_runtime.go:80`, `:142`, and
  `pkg/state/event_claim_renewal.go:40`.
- **Trigger:** A half-open circuit's routing probe takes longer than its
  initial one-minute claim, while the scheduler successfully renews that claim.
  Another recipient for the same consumer checks the circuit after the
  original expiry and before the renewed claim expires.
- **Defect:** `circuitReserve` copies the original claim expiry into
  `Runtime.ProbeUntil`. Recipient renewal extends the durable routing claim
  but never extends this circuit deadline. Circuit advancement treats the
  still-owned probe as lost and reopens it with `probe_timeout`.
- **Evidence:** A probe claimed at T+4 had its claim renewed at T+40 through
  T+100. At T+70, its receipt still showed a live lease and the contender was a
  different claimed outbox row. Nevertheless, the contender's permit check reopened
  the breaker with `reason:probe_timeout`, returning `circuit_open`.
  Both stores reproduced this without waiting for real-time lease expiry.
- **Impact:** A healthy, slow probe loses its recovery opportunity even though
  the worker remains alive and owns its claim. The admission gate can then
  refuse that probe, delaying recovery or keeping long probes in repeated
  false timeout/cooldown cycles.
- **Suggested fix:** Tie probe liveness to the current matching routing claim,
  including renewal, token and generation, or renew both deadlines atomically.
  Preserve timeout recovery when the worker actually loses its claim.

### ED013 NATS token credentials leak through retained consumer errors

- **Priority:** P1. **Status:** Open, reproduced with sealed credentials,
  production poller construction, and health persistence in both stores.
- **Code:** `pkg/sched/poller_nats.go:115`,
  `pkg/sched/dispatch_triggers.go:2168`,
  `pkg/sched/broker_trigger_lanes.go:355`,
  `pkg/apid/apidserver/handlers_triggers.go:1229`, and
  `pkg/apid/apidserver/server.go:3599`.
- **Trigger:** A trigger uses supported token-style credentials in
  `nats://TOKEN@host` or `tls://TOKEN@host`, and the connection attempt fails.
- **Defect:** The constructor formats the URL using `url.URL.Redacted`, which
  masks a password but leaves the username. In token-style NATS URLs, that
  username is the secret token. The error is returned to scheduler logging
  and copied verbatim into durable trigger health. The metrics endpoint
  projects `LastError` without credential redaction and admits `apps:read`.
- **Evidence:** Synthetic credentials were sealed before trigger creation and
  opened only by the real scheduler construction path. An egress-denied local
  destination caused a connection failure; the token appeared in both the
  returned error and stored `LastError`, in MemStore and PostgreSQL. The
  password-style control remained redacted in both stores. No real broker or
  production credential was used. The logging and HTTP response projections
  were traced in code; the test did not start a full API server.
- **Impact:** Connection failures turn encrypted broker credentials into
  plaintext diagnostic data available to log readers and read-scope API keys.
  Health retains the last error even after a later successful poll.
- **Suggested fix:** Remove all URL userinfo from diagnostic URLs, audit
  wrapped client errors for credential content, and sanitize diagnostic
  persistence and projection. Include historical diagnostic cleanup in the
  remediation plan without deleting unrelated failure evidence.

## Release qualification work

### EDG001 Event GA evidence remains incomplete

- **Status:** Open release qualification work, not a newly discovered runtime bug.
- **Required gate:** `docs/adr/20261029093000-event-ga-gate.md:54`.
- The checked-in V12 and failover reports from 2026-10-29 remain provisional;
  the reference-host throughput and between-host failover evidence is required.
- No deployed events journey was found in `tests/journeys`; template journeys
  explicitly exclude event-worker and queue-worker.
- `docs/customer-pages.json` keeps events, events-operations and dead-letters
  unpublished. The capability catalog still marks events and event-triggers
  as preview.
- Complete these gates before promotion; preview status alone is not a defect.

## Validation and reproduction artifacts

- Existing focused scheduler run: 54 top-level tests passed, zero failed,
  zero skipped, including PostgreSQL-backed cases.
- Existing package suites passed: `pkg/events`, `pkg/eventcontract`,
  `pkg/triggerconfig`, `pkg/apid/appevents`, and `pkg/apid/dataapievents`.
- Five new regression checks failed for ED001–ED004: two checks cover ED001.
- Four additional regression scenarios reproduced ED005–ED008 in both
  MemStore and real PostgreSQL, with no skipped tests. ED006 checks both
  single and batch publication; the recorded run has ten failing leaf cases.
- Four recovery regression cases reproduce ED009 across both retry surfaces
  and both stores. ED010 has four ingress regression failures across both
  stores, two bridge transport failures, and a passing valid-identity control.
- Follow-up baseline: 12 existing broker/scheduler tests and 10 bridge tests
  passed with zero failures and zero skips.
- ED011–ED013: six failing leaf cases reproduce all three defects in both
  stores, with two passing password-redaction controls and zero skipped cases.
- Follow-up routing-control baseline: six existing scheduler tests passed,
  including PostgreSQL routing recovery, plus the full `pkg/triggerconfig`
  and `pkg/apid/appaccess` package suites. Scheduler cases had zero skips.
- Retained harness and outputs:
  `docs/ops/evidence/20261111-event-driven-investigation/`.
- Tests use Go's overlay mechanism; production files are not edited.
- AMQP/RabbitMQ adapter defects are excluded because the manifest deliberately
  rejects those trigger kinds. Accepted-event loss within asynchronous standby
  replication lag is also excluded: the GA ADR explicitly accepts and documents it.

## Reviewed leads not counted as defects

- Trigger handlers' local account checks initially appeared to omit company
  membership. Shared `appaccess.Wrap` resolves indirectly addressed trigger
  apps through `pkg/apid/appaccess/indirect.go:128` and authorizes them before
  dispatch. This is not an additional company-access finding.
- NATS token URL authentication itself is supported: the NATS client reads
  URL userinfo ahead of the separate user/password options. ED013 concerns
  diagnostic redaction, not failure to authenticate with token-style URLs.
