# Gregale Commit — internal implementation

This feature is not qualified for customer use. The current implementation
contains the customer transaction helper, polling relay library, and a
PostgreSQL-backed durable acceptance API. The scheduler relay is internally gated; source-bound credential registration,
pause/resume, and transaction helpers are implemented. Blocked-event snapshots, durable replay requests, accepted-row cleanup,
SDK/CLI source management are implemented. Strict Linux process acceptance
and native KVM correctness acceptance passed on 2026-10-02 UTC for the managed
Operations target on the GCP internal test node. The remaining
production qualification work below still prevents customer promotion.


## Version 2 business keys and customer routing (ADR-589)

Version 1 sources continue to use an account-scoped source-wide queue. Opt in
when creating a new source to use a business key:

```sh
gregale commit add orders --name order-events --operation-policy orders \
  --contract-version 2
```

The policy must be an active account-scoped queue policy containing `orders`.
For a trusted producer database that serves multiple customers, use a
`platform_tenant` queue policy and explicitly grant customer selection:

```sh
gregale commit add orders --name customer-order-events \
  --operation-policy customer-orders --contract-version 2 \
  --allow-tenant-selection
```

The grant authorizes every writer of this outbox to choose customers within the
owner account. Use a trusted application backend to write the outbox. Customer
identity in ordinary event data does not confer routing authority. Selected
customers must be active and linked to the fixed application by an active tenant
surface. Source application, policy, contract and grant are immutable.

Deploy the updated `apid` and `schedd` to every serving node before creating
version 2 sources. Pause those sources before rolling either daemon back; older
binaries do not enforce the version 2 routing contract.

Install the fresh schema in `pkg/commit/schema.sql`. For an existing outbox,
explicitly apply `pkg/commit/schema_routing_upgrade.sql` before binding a version
2 source. Existing version 1 sources need no upgrade. Rebinding is an owner
migration: drain the old source and preserve its receipts before moving a database
to a new source; automatic source upgrades are unsupported.

Write business changes and this event in the **same PostgreSQL transaction**:

```ts
await insertCommitEvent(transaction, {
  id: stableEventId,
  type: "order.created",
  data: { order_id: "order-123" },
  routing: {
    version: 2,
    platform_tenant_id: customerId,
    key: "order-123",
  },
});
```

Omit `platform_tenant_id` for account-scoped version 2 sources. Version 2 requires
routing and a nonempty string, number or boolean key. The canonical key has the
existing 256-byte work-policy bound. Same policy/customer/key shares a queue across
sources; different customers or business keys have separate lanes. Numeric values
such as `1` and `1.0` share a lane, while the string `"1"` is distinct. Queueing
coordinates operation ownership; external side effects still need idempotency.

The Go SDK accepts `CommitEventRequest.Routing` and Python accepts
`insert_commit_event(..., routing={"version": 2, "key": "order-123",
"platform_tenant_id": customer_id})`. All helpers insert through the caller's
existing transaction and leave commit/rollback to the application.

A receipt fixes the event type, payload **and routing**. A changed customer or key
for the same source/event conflicts. Accepted retries return the original receipt
before current source, customer, policy or release checks; source pause and
customer suspension do not erase acceptance history. New work remains subject to
those checks. This is the admission slice of application-level durable operations;
managed effect delivery and a dedicated operation timeline are subsequent work.

## Transaction contract

Install `pkg/commit/schema.sql` explicitly in the customer database. Insert the
business write and `commit.Insert(ctx, tx, event)` using the same PostgreSQL
transaction. The application owns commit/rollback. `created_at` records insertion
time, not commit time. The platform relay observes only committed events.

The relay claims bounded batches with expiring, fenced leases. Remote acceptance
runs outside the customer transaction. A lost response or checkpoint retries
the same source/event identity. Gregale commits the receipt and managed Operation
together and returns the original receipt for identical retries. Changed type
or JSON content conflicts. Identity records currently have no expiry and survive
operation/result retention; deleting the source/account deletes its identity history.

Delivery is at least once. Consumers must transactionally record event IDs
alongside their business effects. Acceptance does not imply execution completion.
New receipts carry `operation_id` and link to `/v1/operations/{operation_id}`.
The managed Operations owner supplies pending/running/terminal status, ownership
generation and results. Retained Commit facts remain readable after owner cleanup.
Historical internal receipts keep their original `invocation_id`.

## Current API

- `POST /v1/apps/{slug}/commit-sources` creates a fixed destination source from
  `{"name":"orders","operation_policy":"orders"}`. A connection is sealed through the endpoint below; the managed relay enforces operator-defined network policy and qualifies the source table.
  Names are unique within an account. Registering the same name, application and policy
  returns the existing source; using that name for another destination returns
  HTTP 409 with `commit_source_name_conflict` and preserves the destination.
- `PUT /v1/commit-sources/{source}/connection` seals a TLS-verified PostgreSQL `connection_url` with source-bound encryption. Credentials are never returned.
- `PATCH /v1/commit-sources/{source}` sets `enabled` for pause/resume. Existing receipts remain recoverable while paused.
- `POST /v1/commit-sources/{source}/events` accepts
  `{"id":"<uuid>","type":"order.created","data":{"order_id":"one"}}`.
- `GET /v1/commit-sources/{source}/events/{event}` recovers its acceptance receipt.

Version 1 rejects tenant-required destinations. Version 2 permits them with
explicit owner-authorized customer routing. Customer-supplied tenant payload
fields must never confer identity.

## Qualification work remaining

1. Verify operator trust-root deployment and DNS destination changes. The full
   scheduler process gate passed with its private TLS PostgreSQL source on
   `gregale-internal-test-1` (`gregale-prod`, `us-east1-b`) on 2026-10-01.
   It used the actual scheduler relay, verified producer-death recovery and
   rollback invisibility, and checked for duplicate work after scheduler restart.
2. Verify metrics and source-health behavior under sustained production outages;
   configure operational alerts and audit bounded history and cleanup settings.
3. Qualify the extended combined HTTP consumer scenario, including competing
   relays, relay checkpoint interruption, and poison-event repair/replay.
   The strict process gate requires these assertions; passing the gate in an
   isolated environment does not qualify sustained production behavior. Producer
   death, producer rollback, consumer crashes before and after its business
   commit, source database outages, credential rotation, lost HTTP responses,
   scheduler restart, and concurrent consumer
   duplicates already pass with real databases and daemon processes. The relay
   lease, acceptance-response loss, TLS outage, and blocked replay cases also
   pass in their database/API integration gates; the complete combined fault
   matrix is still a production qualification task.
4. Audit quota races with other invocation producers, source lifecycle, receipt
   storage growth, and version 2 customer destinations. Customer routing has
   database/API tests; native consumer qualification remains required.

Integration tests cover the relay's database behavior and concurrent receipt
creation. The process and native gates below additionally verify scheduler
delivery and Firecracker execution in an isolated qualification environment.

## Revision qualification

The native/process-qualified revision was based on main commit
`a3e1800e37962a3341ef13703b28a3d3c628191b`. The current managed Operations
qualification uses source archive SHA-256
`33bfb31f41301ba90a8e160e8b9ddeb16a318870469fc0381daabeb6eada7703`.
Its 46 changed files were verified before execution. The forward migration
uses the repository's 17-digit timestamp format. The checked-in PostgreSQL
schema was regenerated from live migrations; sqlc regeneration matches.

On 2026-10-02 UTC, all 22 required PostgreSQL/Linux process gates passed on
`gregale-internal-test-1` in project `gregale-prod`, zone `us-east1-b`.
Go, Node and Python transaction helpers passed against real PostgreSQL.
The actual scheduler relay delivered a committed event after producer death,
ignored rollback, preserved its receipt through restart, and completed one
managed Operation without creating a legacy invocation. The HTTP consumer
survived crashes before and after its business commit; eight concurrent duplicate
requests preserved one business effect. Source outages and credential rotation
were included. Receipt insertion failure rolled back owner admission; concurrent
acceptance, immutable policy bindings, policy retirement, explicit customer
schema upgrade and retained completion facts passed their database gates.
The process unit exited successfully, leakcheck passed, and the disposable
PostgreSQL cluster stopped cleanly.

All 11 native source-deployment scenarios passed, including both Commit
producer-death profiles completing real managed Operations after snapshot restore
and cold boot. The Operations owner/policy regression suite passed 10 parent
tests; the wake suite passed 16, including cancellation, concurrent park requests,
and retained deployment pins. Required scenarios had no failures or skips.
Pre/post native leakcheck passed, and PostgreSQL stopped cleanly.

Managed dispatch now uses the coordinated app wake lifecycle, including
activation, rollback, and park/account fencing. The regression reproduces the
original direct-wake defect for both current and retained revisions: it left the
app in `evicted_cold`, allowing lifecycle reconciliation to cancel its cold boot.
The corrected path passes the same checks and the real KVM cold-boot scenario.

The evidence bundle SHA-256 is
`01856570ac7c626b6e015a20cddc78938f4d318a80f97c49f3fa3e0332bf1791`.
It retains test JSON, logs, source/binary/kernel hashes, runner scripts, earlier
failed attempts, and systemd completion journals. The completed transient units
were garbage-collected; each journal verifies PID 1's successful completion for
the same invocation that started the unit. The snapshot cache used bounded
8 GiB tmpfs in a private mount namespace; this proves correctness and does not
qualify the reference SSD latency target. After testing, this summary was updated
and one extra trailing newline was removed from the forward migration. All other
runtime and client files still match the qualified manifest.
Before publication, main commit
`9de1907bd` was integrated to preserve the newer gateway, mirror and MCP
contracts. The Commit decision was renumbered to ADR-430 because main had
allocated ADR-428. PR checks cover the integrated revision; the archived
native/process evidence continues to describe its original source manifest.
Publication checks also added migration replay guards, the CLI's existing
regular-file credential guard, explicit cleanup/error handling, and the missing
API parity and fixture environment declarations. PostgreSQL replay validation
covers an accepted managed receipt and its completion trigger after ledger loss.
Customer promotion remains gated by the production qualification work above.

## Scheduler configuration

Set `FAAS_COMMIT_RELAY_ENABLED=true` only in an isolated qualification environment.
Supply exact comma-separated `FAAS_COMMIT_DATABASE_HOSTS` and operator-approved
`FAAS_COMMIT_DATABASE_CIDRS`. The fleet age identity decrypts credentials;
the scheduler loads it alongside host identities and rotation keys. API
registration uses the public recipient at `FAAS_FLEET_AGE_RECIPIENT_PATH`.
Provision the matching `fleet.age` in the scheduler's age-key directory before
enabling registration.
The relay discovers configured sources in bounded keyset pages, opens at most
two customer connections per source, and closes each source pool after its tick.
Each connection/reconnection resolves DNS and checks the actual IP against the
operator policy. TLS verifies the original hostname. Credential changes are
picked up on the next source pass; no plaintext credential is exposed by APIs.

`schedd_commit_relay_cycles_total{result}` and
`schedd_commit_relay_accepted_total` report bounded health codes and checkpoints.
Customer database errors and connection strings are excluded from logs.

The Node SDK exports `insertCommitEvent`; Python exports `insert_commit_event`.
Both insert using the existing customer transaction and leave commit/rollback to
the application. Using an autocommit connection defeats business-write atomicity.

`TestE2E_CommitProducerDeathReachesCompletedOperation` exercises real Linux
processes and the VMMD protocol fixture. macOS cannot run the daemon boot checks;
this test does not replace the native KVM qualification gate.

## CLI setup

```sh
gregale commit add order-worker --name orders --operation-policy orders
gregale commit connection <source-id> --file /private/path/database-url
gregale commit pause <source-id>
gregale commit resume <source-id>
gregale commit info <source-id>
gregale commit doctor <source-id> --file /private/path/database-url
gregale commit inspect <source-id> <event-id>
gregale commit wait <source-id> <event-id> --until completed --timeout 2m
gregale commit receipt <source-id> <event-id>
```

Create a policy JSON file containing `scope: "account"`, `contention: "queue"`,
`member_app_ids: ["<order-worker-app-id>"]`, `lease_seconds: 15` and
`max_attempt_seconds: 60`. Save it through
`gregale operations policy upsert --file orders-policy.json orders` before adding
the source. Omit `environment_id` for this release. Before registering the connection, install `pkg/commit/schema.sql` in the
customer database as its owner. An older internal outbox needs the explicit
owner upgrade in `pkg/commit/schema_operations_upgrade.sql`; the relay refuses
a schema without the managed `operation_id` checkpoint column. The upgrade
preserves legacy accepted identities and refuses unknown delivery constraints. Bind the outbox to the source ID returned by
`commit add`, and grant the dedicated relay role these table permissions:

```sql
INSERT INTO public.gregale_commit_binding(source_id) VALUES ('<source-id>'::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.gregale_outbox TO gregale_relay;
GRANT SELECT ON public.gregale_commit_binding TO gregale_relay;
```

Create the login role and provide its credential through your existing secret
management. It must connect with `sslmode=verify-full`. Operators configure the
approved hostname, address prefixes, and trusted PostgreSQL CA before enabling
the relay. The owner controls the source binding; relay credentials cannot
change it. Each customer database supports one bound source in this release.

The connection file is read with an 8192-byte cap and its contents are never
printed. Source registration replays the same destination/name without changing
its credentials or paused state. Receipt recovery reports durable acceptance;
use the referenced managed Operation to inspect execution status.

Source info includes the latest relay health, observation timestamp, pending and
blocked counts, and oldest pending insertion time. Unavailable database scans
report unknown counts rather than zero. Credential revision fences stale status
writes. These are observations, not a claim that every source row is accepted.

`doctor` combines the latest scheduler observation with optional local read-only
TLS, schema, binding and relay-role permission checks. The credential file is
never uploaded or printed. Local connectivity does not prove scheduler network
access or production qualification. Failed or unknown checks return exit 1.

`inspect` joins existing source, receipt, retained Operation and bounded blocked
snapshot reads. A missing receipt leaves acceptance and the customer transaction
outcome unknown; it never implies rollback. A blocked snapshot establishes only
that the relay previously observed the committed row. Stale snapshots do not
establish a current block. Durable acceptance/completion takes precedence.
`wait` defaults to completion with a two-minute timeout; `--until accepted` stops
at durable acceptance. Completion returns exit 0; a fresh block or terminal
failure returns exit 1. Timeout/interruption preserves durable work and the last
observed facts. No command claims or publishes an event as part of diagnosis.

Rotation and transition from paused to enabled invalidate prior health and
increment the observation fence. A pass started before either transition cannot
overwrite newer source health or blocked snapshots. Source observations older
than five minutes have unknown backlog totals.

The scheduler also exposes shared-ledger aggregate gauges for enabled, unknown
and failing sources, fresh known pending/blocked counts, oldest pending insertion
timestamp and snapshot-query success. They have no customer identity labels.
Use `max` across schedulers, because each reads the same durable source ledger.
Known counts are partial if any sources are unknown. Paused/deleted sources are
excluded; a failed ledger read does not retain old backlog gauges. The five
Commit alert rules and [recovery runbook](runbooks/FaasCommit.md) cover old work,
blocked events, unknown source observations, relay failures and unavailable
platform observations. `make commit-alert-check` verifies firing and healthy
suppression cases with promtool. Operator deployment and sustained production
qualification are still required before customer promotion.

`make test-commit` is the strict PostgreSQL/Linux process gate. It rejects missing
configuration, macOS, disabled PostgreSQL tests, skipped required scenarios, and
failed tests. Native Firecracker acceptance remains an additional release gate.

The managed relay deletes at most 32 accepted customer outbox rows per source pass,
after seven days by default. Pending and blocked events are never deleted by
cleanup. Gregale receipt identities remain separate from customer cleanup.

Blocked-event snapshots contain at most 32 event IDs/types/reason codes, never
payloads. `GET /v1/commit-sources/{source}/blocked-events` exposes this observation.
After correcting an unaccepted source row, request replay through
`POST /v1/commit-sources/{source}/events/{event}/replay`. The durable request waits
for the source relay, preserves the event ID, and is fenced by generation.
Acceptance remains at least once; the worker must deduplicate business effects.

Set `FAAS_COMMIT_API_ENABLED=true` only for qualification. Disabling it blocks new
source mutations and submissions while leaving existing receipt/status reads
available. Scheduler delivery is independently gated by
`FAAS_COMMIT_RELAY_ENABLED`.

`make test-commit-native` runs the source-deployment fixture plus producer death,
Commit-driven snapshot restore, and Commit-driven cold-boot completion. It requires
native x86_64 Linux KVM, root, qualified kernel/builder inputs, and disposable
PostgreSQL. Required subtests must pass; skipped subtests fail qualification.
Build daemon binaries with `-tags metal`. For an isolated local storage root,
the legacy `/srv/fc/snap` device-state locator must resolve to the same snapshot
objects as the storage backend. The GCP runner provides this mapping inside a
private mount namespace. Capacity must include temporary private-drive copies
while publishing replacement captures; final artifact size understates peak use.
The current managed Operations target passed the full GCP native gate:
source deployment, public HTTP readiness, snapshot HTTP wake, forced cold boot,
corrupted-snapshot fallback, both Commit producer-death wake profiles, and cleanup.
VMMD now fences exit notifications by process attempt so a retired restore
cannot reject its healthy cold-boot replacement. The corruption fixture also
verifies that parking publishes a new usable capture and that it restores
successfully. Injected snapshot changes are restored between cases.

The managed relay reads `public.gregale_outbox`. Install the documented schema in
`public`; the relay pins its search path and qualifies the table and event-ID
primary key before processing. The database role needs SELECT, INSERT, UPDATE,
and DELETE permissions on that table.

Acceptance receipts include `operation_url`. Read that URL or run
`gregale commit operation <operation-id>` to distinguish accepted, running,
completed, cancelled, and failed work. Execution state is copied into the durable
receipt in the same database transaction as managed owner state changes, so it
survives operation/result retention. This interface remains under qualification.

Commit targets request-serving apps through the managed Operations dispatcher.
Version 1 sources fix an active account-scoped queue policy with no environment pin
and containing its application. The source UUID defines one serialization lane;
the event UUID defines owner idempotency. Policies control leases, retry delay and
attempt limits. The existing account lock and pending-operation quota serialize
admissions with other managed work. Enabled sources prevent policy retirement
and incompatible changes. Pause the source and finish outstanding work before
retiring its policy. A retired or incompatible policy cannot be resumed.
Worker/job destinations remain unsupported. Version 2 customer sources permit
tenant-required request applications under ADR-589.

## Consumer transaction recipe

For managed HTTP operations with PostgreSQL business writes, use the
[SDK transaction wrapper](operation-transactions.md). It commits the business
writes and complete result/effect response together, then replays that response
for the same operation without rerunning committed work. It checks the trusted
account/app/customer scope and exact request input, including across deployment
or generation changes. Install the receipt schema explicitly and retain receipts
while their operations can replay.

For consumers that own their event identity and receipt implementation:

Use a consumer-owned table with `PRIMARY KEY (consumer, source, event_id)`. In the same
transaction as the business effect, insert the stable CloudEvents source/ID pair using
`ON CONFLICT DO NOTHING`. Apply the effect only when the insert affected one
row, then commit before returning a successful HTTP response. A rollback must
remove both the effect and its deduplication marker. Separate consumers use
separate names. Distinct sources may legitimately use the same event ID.
Do not expire markers while the event can still be replayed.

`pkg/commit/consumer_example_test.go` exercises this recipe with a failed
transaction followed by eight concurrent deliveries and verifies one committed
effect, plus an independent effect for the same ID from a different source.
This proves the PostgreSQL transaction recipe, not VM execution or
idempotency of external side effects.

The process acceptance suite also requires `FAAS_COMMIT_TLS_PG_BIN_DIR`, pointing
to the directory containing `initdb` and `pg_ctl`. Run it as an unprivileged
user, or set `FAAS_COMMIT_TLS_PG_USER` to an existing unprivileged user when the
harness runs as root. The native gate requires that user explicitly; temporary
PostgreSQL processes drop to it while the native runtime retains root. The TLS test creates and removes its own PostgreSQL cluster; it does not
change the database selected by `DATABASE_URL`. It checks verified hostnames,
network-policy denial, pinned search path, source relay handoff, database
password rotation, outage/restart recovery, and retained source checkpoints.
The fixture supplies its CA through operator-owned `PGSSLROOTCERT`, rather than
allowing customer URLs to select trust roots. Production operators must provide
a trust store appropriate for their qualified sources.

The producer-death process test first rolls back a paired order/outbox write and
verifies both rows are absent. It pauses the source before committing. After the
producer is killed it resumes the source through the API, waits for the actual
scheduler relay to accept and checkpoint the event over verified TLS, waits for
execution completion, and restarts the scheduler. A fresh source-health update
then proves the restarted relay scanned the source without creating another
logical invocation and that the rolled-back event has no receipt. Both native
wake profiles use this same relay path.

## SDK transaction acceptance

The Go SDK exposes `InsertCommitEvent(ctx, tx, event)` for an existing
`*sql.Tx`; it returns the supplied or generated UUID. Native pgx applications
can use `commit.Insert` from the server module. Node exposes
`insertCommitEvent(client, event)` and Python exposes
`insert_commit_event(cursor, event_type, data, event_id=...)`. All inserts name
`public.gregale_outbox` explicitly, so an existing business search path cannot
divert events into a different table. They leave commit and rollback to the
application.

Run `make test-commit-sdk` with `DATABASE_URL` pointing at a disposable cluster
whose role has CREATEDB permission. Each language creates and drops a private
database, with both public and business-schema outboxes to test qualification.
Install Node development dependencies with `npm ci --ignore-scripts` in
`sdk/node`. Install the Python SDK and `sdk/commit-tests/requirements.txt` in
your test environment; `GREGALE_COMMIT_PYTHON` selects its interpreter. The Go
acceptance driver lives in a separate module so the distributed Go SDK keeps
its standard-library-only dependency contract. `make test-commit` includes
these SDK checks before the Linux process acceptance suite.

## Fixed database source binding

After registering the source and installing the customer schema, the database
owner must bind the outbox to the returned source ID:

```sql
INSERT INTO public.gregale_commit_binding(source_id)
VALUES ('<source UUID>'::uuid);
```

Grant the relay role SELECT on `public.gregale_commit_binding`, in addition to
its delivery-state permissions on `public.gregale_outbox`. The relay reads the
binding and cannot change the destination. Missing or mismatched bindings show
`source_binding_unqualified` in source health; those sources do not claim,
replay, or clean up events. The managed first release supports one fixed source
per database outbox, including when the same database has multiple credentials
or DNS aliases. Do not rebind a live outbox: pause delivery and use a separately
provisioned outbox/database for a new source.

## Deliver operation effects

Managed HTTP handlers can return a versioned result envelope that atomically
queues signed webhook effects with completion. Customer-scoped Commit operations
can target only explicitly subscribed receivers for that same authenticated
customer, with an active linked app surface. See
[managed operation effects](managed-operation-effects.md) for handler examples,
receiver configuration, delivery status, and upgrade requirements.

## Version 3 application-event publication (ADR-935)

This destination remains under Commit's existing operator qualification gates.
Bind a **new** source to an account-scoped publisher application:

```sh
gregale commit add invoices --name invoice-publications \
  --destination event --contract-version 3
```

The API accepts `destination: "event"`, `contract_version: 3`, and a source name.
Omit `operation_policy` and `allow_tenant_selection`. The source is immutable;
reusing a name with a different destination or app conflicts. The bound app does
not need a request-serving deployment because it publishes rather than receives
an invocation. Tenant-required publisher applications and per-row routing are
unsupported. Existing version 1/2 sources continue invoking managed Operations.

Install the fresh `pkg/commit/schema.sql`, or explicitly run
`pkg/commit/schema_publications_upgrade.sql` as the database owner against an
existing managed outbox. Older invocation-only outboxes first need their existing
managed-Operations upgrade. Bind `public.gregale_commit_binding` to the new source
UUID, seal its connection using `gregale commit connection`, and configure the
existing scheduler relay as described above. The relay never installs DDL.
Version 3 schema qualification requires `schema_version` and
`publication_acceptance_id` checkpoint columns.

Inside the business transaction—or the transactional consumer callback—insert
an ordinary Commit event with its stable UUID, type and JSON data. All three SDKs
support an optional registered schema version:

```go
id, err := faas.InsertCommitEventInTransaction(ctx, tx, faas.CommitEventRequest{
    Type: "invoice.paid", Data: json.RawMessage(`{"invoice_id":"123"}`),
    SchemaVersion: "v1", // omit for schema-free event types
})
```

```js
const id = await insertCommitEvent(tx, {
  type: 'invoice.paid', data: { invoice_id: '123' }, schema_version: 'v1'
});
```

```python
id = insert_commit_event(cursor, "invoice.paid", {"invoice_id": "123"}, schema_version="v1")
# Async callbacks use: await ainsert_commit_event(cursor, ..., schema_version="v1")
```

Omit schema version for schema-free types. Register versioned schemas against
`app.<publisher app UUID>` before acceptance. Missing/unknown versions and invalid
data become `event_schema_rejected` blocked rows; fix the unaccepted row or source
configuration, then request the existing explicit replay. Accepted identity is
immutable: changed type, schema version or semantic JSON data conflicts. Retrying
accepted rows skips current schema, pause and event-capacity admission checks.
The event-publication body is bounded by the existing 1 MiB application publication
limit; storage accounting includes the captured subscriber snapshot.

Subscriptions match source `app.<publisher app UUID>`. The emitted event ID is
`commit.<source UUID>.<outbox event UUID>`, so distinct Commit sources do not share
producer identity. This differs from the hashed IDs used by direct application
producer-key publication. It uses the same subscription filters, acceptance-time
snapshot, per-consumer ordering, delivery recovery and event retention rules.
Schema version appears as the CloudEvents `schemaversion` extension.

`gregale commit receipt SOURCE EVENT` returns `publication` instead of an
`operation_id` or `invocation_id`. It contains `acceptance_id`, event `source` and
`id`, exact `accepted_at`, and a guarded `receipt_url`. Read that URL for the
original acceptance's paginated subscriber routing/execution/recovery evidence;
follow `next_after` while preserving source, ID and `expected_accepted_at`. A
replacement acceptance returns 409 rather than evidence for the wrong event.
Node/Python generated event receipt clients support this guard; Go's
`GetCommitPublicationReceipt` takes the publication descriptor and page options
(the standalone SDK preserves subscriber rows as lossless JSON).

`gregale commit inspect SOURCE EVENT` includes the first bounded subscriber page.
`gregale commit wait SOURCE EVENT --until accepted` waits for publication
acceptance. There is no single managed Operation completion for a publication;
inspect each subscriber independently. Event receipt pruning returns unavailable
delivery evidence, while the retained Commit receipt still proves original
acceptance. Even then, a late relay retry returns that same receipt without
recreating fanout work. Customer cleanup removes accepted outbox rows under the
existing policy and never expires the platform's Commit identity.

Business writes and outbox insert commit together in the customer database;
event acceptance, captured recipients and Commit receipt commit together in the
platform database. Those two transactions are separate. The relay may publish
before the incoming consumer HTTP response arrives. Publication acceptance does
not imply subscriber success, and at-least-once consumers still require durable
idempotency. No cross-subscriber execution order is introduced. Relay claims can accept rows
in a different order from business transaction commits; version 3 does not impose
a producer commit-time order. Configure existing subscription ordering keys for
the required per-consumer delivery lanes.

### Publication acceptance history

For a version 3 event source, discover accepted work without knowing each outbox
UUID first:

```sh
gregale commit history SOURCE --limit 50
gregale commit history SOURCE --limit 50 --before NEXT_BEFORE
gregale commit inspect SOURCE EVENT
```

`GET /v1/commit-sources/{source}/publications` returns metadata-only `items`
containing the Commit receipt, original event type, optional schema version and
publication descriptor. It excludes event data. Go `ListCommitPublications`,
Node `QueuesService.listCommitPublications`, and Python
`faas_sdk.api.queues.list_commit_publications` expose the same read.

Pages sort by acceptance time and receipt UUID descending, default to 50 items,
and allow up to 200. Pass `next_before` unchanged for the next page; an absent
cursor marks the last currently observed page. Cursors are source-scoped.
These are live reads, not a frozen snapshot or a change feed: concurrent
transactions can become visible behind a page already read, so restart from the
first page when reconciling recent admissions. This order does not establish
producer transaction order or subscriber execution order.

History remains available for paused sources and after ordinary event evidence
retention. Its publication `receipt_url` includes the acceptance-time guard for
reading each subscriber's independent delivery results. A 404 means delivery
evidence is unavailable; a 409 means a replacement acceptance must not be
attributed to the retained receipt. Neither means the original Commit acceptance
was undone. Use `commit inspect SOURCE EVENT` to combine retained acceptance
with available delivery evidence. An accepted history item alone does not prove
subscriber completion. Operation-destination sources are rejected by this read.

### Wait for an individual publication subscriber

```sh
gregale commit wait SOURCE EVENT --subscription-id SUBSCRIPTION_UUID \
  --until completed --timeout 5m --interval 1s
```

The CLI first waits for durable publication acceptance, then pins all recipient
reads to that original acceptance time. The single timeout covers both stages.
It follows recipient pagination and selects only the named application
subscription captured at acceptance. Backfill recipients, workflow recipients
and generic replay recovery executions do not satisfy this wait. In-place
retries/replay of the original invocation can update its observed state, but the
wait returns immediately when a currently terminal outcome is observed; it does
not wait for a future operator recovery action.

Only `completed` means the original invocation finished successfully. Other
outcomes include `failed`, `dead_letter`, `cancelled`, `expired`, `superseded`,
`routing_failed`, `filtered`, `not_targeted`, `unsupported_recipient`,
`unavailable`, and `acceptance_replaced`. Receipt pruning is unavailable evidence,
never success. Routing enqueue is not execution completion. A completed
invocation does not independently prove an external service's side effects.

The CLI prints the selected recipient evidence and exits 0 only for `completed`,
1 for another outcome or read failure, 124 for timeout, and 130 for interruption.
Timeout/interruption stops reads and does not cancel durable work. Transport and
server failures remain errors rather than successful delivery outcomes.

SDK equivalents accept a saved `CommitEventPublication` descriptor:

- Go: `client.WaitCommitSubscriber(ctx, publication, subscriptionID, time.Second)`.
  Set the context deadline; cancellation/deadline returns the last observation
  with `interrupted`/`timed_out` and a context error.
- Node: `waitCommitSubscriber(publication, subscriptionID, { timeoutMs: 300000,
  intervalMs: 1000, signal })`, using the configured generated `EventsService`.
  Timeout/interruption returns a result with the corresponding status and aborts
  the current read. Other read errors reject the promise.
- Python: `await await_commit_subscriber(client, publication, subscription_id,
  timeout=300, interval=1)`, with an authenticated generated client. Timeout
  returns `timed_out`; task cancellation propagates `CancelledError`. Other
  read failures propagate.

SDK readers preserve the original acceptance-time guard on every page. Each
result concerns one subscriber independently; it does not assert completion of
other consumers or require them to finish first.

### Delivery report across recent publications

```sh
gregale commit delivery-report SOURCE --limit 50 --timeout 2m
gregale commit delivery-report SOURCE --subscription-id SUBSCRIPTION_UUID
gregale commit delivery-report SOURCE --before NEXT_BEFORE --limit 50
```

This read-only report selects one newest-first publication-history page (default
50, maximum 200 publications) and reads guarded recipient pages for each
publication (up to 200 recipients per page; one page by default). Each item retains its acceptance receipt and
reports `evidence_status`: `available`, `partial`, `unavailable`,
`acceptance_replaced`, `not_read`, or `read_error`. Subscriber rows include the
original recipient evidence, including existing recovery and attempt-history
links. It does not request replay, cancel work or publish events.

`counts` summarizes only the original subscribers actually observed, plus
`not_targeted` when a requested subscription is absent from a complete captured
recipient snapshot. It excludes backfills and does not adopt generic replay
success as original execution success. Workflow recipients are reported as
`unsupported_recipient`. Completion retains the same meaning and limitations
as individual subscriber waits.

`next_before` continues publication history. An item's `next_after` means its
recipient coverage is incomplete; pass it to the guarded publication receipt
reader to continue. A missing requested subscriber on a partial page stays
unknown, rather than being labelled untargeted. Even with no continuation,
a receipt whose original snapshot has not been captured is `partial`.
Unavailable or replaced reads never establish a new completion observation;
a resumed report can retain earlier local observations as described below.

`read_complete` means all selected publication reads finished, including reads
that established unavailable/replaced evidence. It does not mean every
subscriber completed, every recipient was sampled, or every publication in the
source was examined. These are live reads across separate snapshots; counts are
observations rather than an atomic source-wide health snapshot.

The CLI prints partial reports on read errors and exits 1 (124 for deadline,
130 for interruption). Unvisited selected publications remain `not_read`.
Exit 0 means the reads finished, even when subscribers failed. After a read
error, resume the saved report or retry the same history cursor; advancing `next_before` would skip unread
items in that selected history page.

SDK equivalents:

- Go: `client.GetCommitDeliveryReport(ctx, source, before, subscriptionID, limit)`.
  Use an empty subscription ID for all sampled recipients and set a context
  deadline. The result is returned alongside any read error.
- Node: `getCommitDeliveryReport(source, { before, limit, subscriptionId,
  timeoutMs, signal })`, using configured generated services. A failed read throws
  `CommitDeliveryReportError` with the partial `report` and original `cause`.
- Python: `await aget_commit_delivery_report(client, source, before=cursor,
  limit=50, subscription_id=subscription, timeout=120)`. Read failures and timeout
  raise `CommitDeliveryReportError` with partial `report`; task cancellation
  propagates. Omit `subscription_id` to inspect all sampled original recipients.

For complete evidence of one selected subscriber, use the subscriber wait,
which follows recipient pagination. Reports preserve the acceptance-time guard
on every sampled receipt and remain useful after delivery evidence expires.

### Resume recipient coverage

```sh
gregale commit delivery-report SOURCE --limit 50 --recipient-pages 2 > report.json
gregale commit delivery-report SOURCE --resume report.json --recipient-pages 5 > report-next.json
```

`--recipient-pages` bounds each publication to 1–20 recipient pages per run,
defaulting to 1. A resumed run uses the saved selection and subscriber filter;
omit `--before`, `--limit` and `--subscription-id`. It does not select newer
publications or change `next_before`. Save output to a different file from the
resume input. CLI input is limited to 64 MiB; use smaller history selections
for large reports.

Resume authenticates the source with the current account, validates publication
source/event bindings and rejects duplicate saved publication/subscriber
identities. It copies the saved report, skips completed coverage (`available`)
and terminal unavailable/replaced evidence, and continues `partial`, `not_read`
or `read_error` items from their saved recipient cursor. A partial receipt with
no cursor is reread from the first page because its snapshot was incomplete.
Previously read complete coverage is not refreshed.

Each successfully decoded page merges original recipients by subscription UUID.
Repeated recipients replace their saved observation; counts are derived from
the merged records, not from saved count totals. The cursor advances only after
that page has been decoded and merged, so a failed read can be retried without
adding duplicate counts. Repeated/cyclic cursors within a run are read errors.
The same acceptance-time guard remains on every page.

When retention removes a later page, the item becomes `unavailable`. A
replacement becomes `acceptance_replaced`. Earlier local observations remain
in the report for diagnosis and retain their counts; these statuses explain why
coverage could not finish. Such saved counts do not establish current delivery
health. `partial` means coverage still needs work, and `read_complete` still
means the bounded read run finished rather than full coverage or delivery
completion. Saved reports are editable local observations, not signed proof,
and observations collected on different pages can reflect different times.

SDK support preserves existing one-page defaults:

- Go: `GetCommitDeliveryReportWithPages(ctx, source, before, subscription, limit,
  pages)` and `ResumeCommitDeliveryReport(ctx, saved, pages)`.
- Node: `getCommitDeliveryReport(source, { recipientPages: 5, resume: saved,
  timeoutMs: 120000 })`. With `resume`, omit selection/filter options.
- Python: `await aget_commit_delivery_report(client, source, recipient_pages=5,
  resume=saved, timeout=120)`. With `resume`, omit selection/filter options.

Resume errors preserve partial reports using the same SDK error contracts.

### Refresh a saved delivery report

```sh
gregale commit delivery-report SOURCE --refresh report.json \
  --recipient-pages 5 --timeout 2m > report-refreshed.json
```

Refresh rereads the same saved publication selection and subscriber filter from
the first recipient pages. It preserves the original acceptance-time guards,
checks the source under the current account, and uses the same 1–20 page budget
and 64 MiB CLI input limit as resume. `--refresh` and `--resume` are mutually
exclusive; omit `--before`, `--limit` and `--subscription-id` with either mode.
Write refreshed output to a different file from the input.

Refresh clears previous recipient coverage and counts before reading. Its
counts describe only newly observed subscriber outcomes, including on a partial
read failure. Saved successes are not carried into unread or unavailable rows.
Earlier observations remain in the input file. Missing recipients on a partial
page are unknown; refresh does not infer completion, removal or recovery from
absence. It continues to classify original executions independently of generic
replay recovery and backfills.

Every report item now records optional `observed_at`, the client's UTC time of
its latest successful receipt-page read or conclusive unavailable/replaced
response. This is not the application's execution time or a snapshot timestamp
shared by all subscribers. Earlier reports without this field remain valid.

Refresh includes `changes` for differences between saved and newly observed
statuses. `kind: evidence` describes changes in coverage/availability;
`kind: subscriber` describes an observed original subscriber outcome;
`kind: recovery` describes a change in the latest retained replay outcome. Each change has
`event_id`, optional `subscription_id`, `previous_status`, `status`, and available
previous/current observation timestamps. A newly observed subscriber has
`previous_status: unobserved`; that is new evidence, not proof of a new delivery.
Unobserved previous recipients are not reported as removed. Unchanged outcomes
produce no change entry. Changes compare editable local evidence, not signed
platform history. Read errors remain errors and do not become delivery changes.

If refreshed coverage is partial, resume the refreshed output to extend those
fresh observations. Resume omits the preceding refresh's change summary because
it does not retain the earlier comparison baseline. Run another refresh against
a saved baseline when a new outcome comparison is needed.

SDK equivalents:

- Go: `client.RefreshCommitDeliveryReport(ctx, saved, pages)`, returning fresh
  partial observations alongside any error.
- Node: `getCommitDeliveryReport(source, { refresh: saved, recipientPages: 5,
  timeoutMs: 120000 })`; omit selection/filter and resume options.
- Python: `await aget_commit_delivery_report(client, source, refresh=saved,
  recipient_pages=5, timeout=120)`; omit selection/filter and resume options.

All helpers copy the saved report. Existing resume behavior preserves earlier
observations; refresh gives an explicit way to inspect the same selection again.


### Recovery visibility in delivery reports

Report subscriber rows keep `status` as the original delivery outcome and add
`recovery` for the latest retained generic replay. For example, an original
`failed` row can have `recovery.status: recovered` after its latest replay
completes. This does not change original completion waits or prove external
business effects.

Recovery status is `recovered` for a completed latest replay, `pending` for
pending/dispatching, `failed` for failed/dead-letter/cancelled/expired/superseded,
and `unavailable` when replay evidence is missing or its state is unknown.
Workflow recipients remain unsupported. Missing evidence does not mean no replay
occurred: replay records expire independently. An older successful replay does
not override a newer failed or pending replay.

Each recovery summary includes the retained replay count and history URL when
available, plus the latest replay invocation ID and state. Reports reuse the
same guarded receipt pages; no additional history requests or recovery actions
are issued. Backfill recipients remain excluded. `recovery_counts` counts only
summarized sampled rows, separately from original-outcome `counts`. A complete
snapshot's `not_targeted` row has no recovery summary.

Refresh recomputes these summaries and emits `kind: recovery` status changes;
older saved rows without a summary compare as `unobserved`. Resume retains saved
summaries until a row is reread, rebuilds recovery counts, and clears prior change
summaries. As with original outcomes, missing rows on a partial read do not imply
recovery. Use refresh to inspect completed coverage again.

Go exposes `CommitDeliveryReportSubscriber` and `CommitDeliveryReportRecovery`;
Node exports the corresponding interfaces. Python reports expose the same JSON
fields. CLI JSON includes these fields with the existing delivery-report modes.


### Read-only recovery planning

```sh
gregale commit recovery-plan SOURCE --report report.json \
  --recipient-pages 5 --timeout 2m > recovery-plan.json
```

This rereads the saved publication selection and subscriber filter from the
first recipient pages, using the original acceptance guards and current source
ownership checks. It does not trust saved recovery actions, request replay,
follow action URLs, or fetch replay history. The same 200-publication,
1..20 recipient-page and 64 MiB CLI input bounds apply.

Output includes the fresh `report`, subscriber `items`, eligibility `counts`,
and `publication_issues` keyed by publication event UUID. Publication issues
report partial coverage, unavailable/pruned evidence, changed acceptance,
not-read publications, or read errors using report evidence statuses. Missing
subscribers on incomplete pages are unknown, not implicitly eligible or safe to
skip. Counts cover sampled subscriber items only. An empty plan is not proof
that every delivery completed.

Each item is `eligible` with one current server-offered action, or `skipped`
with a reason: `completed`, `delivery_active`, `not_targeted`,
`evidence_unavailable`, `cancelled`, `no_recovery_action`,
`unsupported_recipient`, `unsupported_action`, or `action_identity_mismatch`.
The latest retained replay takes precedence when checking completed/active
execution; the original outcome remains in the report. Workflow recipients
remain unsupported.

Known routing, handler, keyed-handler and dead-letter replay actions must use
POST and match the current consumer/application or execution identity.
Routing bodies must match the guarded publication and selected subscriber;
explicit expiry overrides are not planned. Multiple, unknown, mismatched or
unexpected-method actions are skipped. Dead-letter IDs come from the current
server offer, with the application-scoped URL checked.

Eligibility is a live observation, not an executable authorization or a frozen
snapshot. State can change immediately after reading, and any later apply
implementation must reread and revalidate authorization and eligibility.
Timeout/read failures return partial plans through the Go return value,
`CommitRecoveryPlanError.plan` in Node/Python, and CLI JSON with a nonzero exit.
SDK option-validation errors and cancellation follow the existing report helper
behavior. Saved input reports are copied.

```go
plan, err := client.PlanCommitRecovery(ctx, savedReport, 5)
```

```ts
const plan = await planCommitRecovery(source, savedReport, {
  recipientPages: 5, timeoutMs: 120000,
});
```

```python
plan = await aplan_commit_recovery(
    client, source, saved_report, recipient_pages=5, timeout=120,
)
```

### Applying a reviewed recovery plan

```sh
gregale commit recovery-apply SOURCE --plan recovery-plan.json --yes \
  --limit 50 --recipient-pages 5 --timeout 2m > recovery-result.json
```

Apply copies and validates the reviewed plan, checks current source ownership,
and considers only items marked `eligible`. Each considered item rereads its
original acceptance and subscriber from the first receipt page, then compares
the freshly offered action with the reviewed kind, method, URL and JSON body.
Saved actions are never dispatched directly. Changed, completed, active,
unavailable, unsupported and unreviewed items are skipped. The original
publication selection and subscriber filter remain fixed.

Writes are sequential. `limit` bounds eligible items examined (1..50), including
items later skipped; `recipient-pages` bounds receipt reads for each item
(1..20), and the overall timeout bounds the entire operation. CLI plan files
share the 64 MiB saved-report limit. Node/Python first perform an additional
bounded one-page refresh of the whole saved selection before any write; Go
validates source ownership and saved identities without that extra refresh.
Partial recipient coverage never proves an unseen subscriber is eligible.

Freshly validated handler, keyed-handler, dead-letter and acceptance-guarded
routing replay endpoints are submitted. Routing replay requires the current
server offer to include `expected_accepted_at` matching the original publication.
Older servers without that offer are skipped with `acceptance_guard_unavailable`.
The fresh check and write are separate requests, not an atomic snapshot;
server endpoint authorization/eligibility checks still apply, and state may
change between the requests. Do not treat client checks as a race-free lock.

Results retain the event/subscriber identities and per-item statuses:

- `queued`: the endpoint acknowledged the request; this is not delivery completion.
- `skipped`: no write was submitted, with a reason such as `action_changed`,
  `completed`, `delivery_active`, `acceptance_guard_unavailable`, or a receipt evidence status.
- `read_error`: eligibility could not be reread; other items can continue.
- `rejected`: an explicit HTTP 4xx response other than 408 rejected the write.
- `unknown`: a transport failure, cancellation during a write, HTTP 408/5xx,
  unexpected response, or unreadable success response makes the result ambiguous.
- `not_attempted`: the apply budget or a stopped batch left the item untouched.

An unknown result stops further writes and requires inspection before retrying.
There are no automatic retries. Execution replay keys remain `commit-recovery-` plus SHA-256 of
`kind + "\n" + url`. Routing keys additionally include the event source/ID,
subscriber, acceptance timestamp and routing generation so sibling
consumers, events, and newly reviewed replay generations do not collide. Server idempotency retention is finite; keys do
not authorize unattended retries or guarantee deduplication after expiry.
Inspect original and recovery evidence before deciding what to do next.

`complete` means the loop finished without interruption or an ambiguous write;
it does not mean all reviewed items were submitted or consumers completed.
Counts cover returned item statuses. CLI exits nonzero on unknown, read-error,
rejected, or not-attempted items. Go returns the partial result alongside errors;
Node/Python expose `CommitRecoveryApplyError.result`. Python wraps cancellation
so a potentially submitted request retains its unknown result. SDK option
validation errors occur before writes. No recovery requests are issued by plan
creation, report reads or refresh.

```go
result, err := client.ApplyCommitRecovery(ctx, reviewedPlan, 50, 5)
```

```ts
const result = await applyCommitRecovery(source, reviewedPlan, {
  limit: 50, recipientPages: 5, timeoutMs: 120000,
});
```

```python
result = await aapply_commit_recovery(
    client, source, reviewed_plan, limit=50, recipient_pages=5, timeout=120,
)
```


### Atomic acceptance guard on routing replay

Selective routing replay accepts optional `expected_accepted_at`. The server
locks the authorized receipt row and compares its exact acceptance instant in
the same transaction that requeues the selected recipient. A replacement yields
409 without replay history or delivery mutation; missing evidence yields 404.
The memory store performs comparison and mutation under its mutex. This guard
protects original acceptance snapshots; historical backfill replay continues
through the existing unguarded path. Omitted guards preserve prior behavior.

```sh
gregale events replay CONSUMER --event-source SOURCE --event-id EVENT \
  --subscription-id SUBSCRIPTION --expected-accepted-at ORIGINAL_ACCEPTED_AT
```

Original-recipient routing recovery offers carry the guard, and Commit apply
requires it to match the saved publication. A plan made before guarded offers
were available must be refreshed and reviewed again: its action body changed.
Routing acknowledgements must match the submitted event/subscriber and report
`pending`; missing or unreadable evidence remains `unknown`, stopping apply.
Explicit delivery-age overrides remain outside automatic Commit recovery apply.

Node requests expose `expected_accepted_at` as RFC3339 text. The Python request
model also preserves the exact text instead of truncating nanoseconds through
`datetime`; Go uses `*time.Time`. Python regeneration reapplies this precision
rule. Full database and production failure qualification remains pending; static
checks and builds do not establish transactional runtime qualification.

### Atomic routing generation guard

Original routing recovery offers include `expected_generation` when the
recipient has a routing generation. Replay checks it alongside
`expected_accepted_at` and failed state in the transaction. A changed generation
returns HTTP 409 with code `event_replay_generation_changed` without mutation.

Commit recovery apply skips stale reviewed generations as `generation_changed`,
including a race caught by the server after the fresh read. Missing generation
evidence yields `generation_guard_unavailable`; refresh and review a new plan.
Routing idempotency keys include the generation instead of observation time.
Legacy and backfill receipts without recipient generation evidence cannot be
automatically applied through this path. Acceptance-only and unguarded manual
API requests retain their behavior.

Manual replay supports `--expected-generation N --expected-accepted-at RFC3339`.
The generation must be nonnegative and the acceptance guard is required.
See [ADR-937](adr/937-routing-replay-generation-guard.md).

### Inspecting recovery results without replay

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --limit 50 --recipient-pages 5 \
  --timeout 2m > recovery-status.json
~~~

Status validates the original source, publication selection and subscriber
identities, then rereads guarded receipt evidence. It submits no recovery
requests. Each item keeps the saved attempt unchanged alongside fresh
recipient evidence, observed_at, current generation, reviewed
expected_generation and generation_relation when available.

Current status is completed, active, failed, unresolved,
unsupported_recipient, read_error or not_observed. It describes the
retained delivery evidence, separately from the saved request outcome.
Retained latest replay takes precedence over original execution; when a saved
acknowledged execution ID matches either execution, that matching execution is
sampled and attribution is matches_acknowledged_execution. Otherwise
attribution is unproven. A generation advance or observed completion never
resolves an unknown attempt as successful. Missing or replaced acceptances,
missing recipients and expired execution evidence remain unresolved.

Counts cover the supplied result items, which may be a subset of the reviewed
plan. Complete means the loop considered all those items without read error,
item-limit truncation or interruption. A receipt-page limit can still leave
recipient evidence unresolved, regardless of whether consumers completed or
requests remain ambiguous. No history URL or saved action URL is followed. Inputs are copied
and saved aggregate counts are ignored. Saved files are editable observations,
not signed proof.

The observation limit is 1..50 and receipt pages per item 1..20. Each CLI input
file is limited to 64 MiB. The overall timeout covers the operation.
Node/Python first refresh one receipt page per publication across the saved
selection for validation; Go validates source and saved selection directly.
Read failures preserve partial output. CLI exits nonzero for incomplete,
unresolved, failed or unsupported observations and any saved unknown attempt.
No automatic replay or retry follows status inspection.

~~~go
status, err := client.GetCommitRecoveryStatus(ctx, plan, applied, 50, 5)
~~~

~~~ts
const status = await getCommitRecoveryStatus(source, plan, applied, {
  limit: 50, recipientPages: 5, timeoutMs: 120000,
});
~~~

~~~python
status = await aget_commit_recovery_status(
    client, source, plan, applied, limit=50, recipient_pages=5, timeout=120,
)
~~~

Go returns partial status alongside a read error. Node/Python raise
CommitRecoveryStatusError with partial output in result.
See [ADR-938](adr/938-read-only-commit-recovery-status.md).

### Resuming recovery status

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --resume recovery-status.json \
  --limit 50 --recipient-pages 5 --timeout 2m > recovery-status-next.json
~~~

Resume preserves successful status reads, including active, failed, unresolved
and unsupported observations with their original evidence and timestamps.
It first reads not_observed items, then retries read_error items, keeping
output in the original apply-result order. Copied observations do not consume
the 1..50 read limit. A retry clears its old partial evidence before rereading.

New status results include each item's selection snapshot: the original
publication descriptor and reviewed plan item. Resume validates these against
the supplied plan and result, including the exact acceptance instant, reviewed
action, saved attempt, source, subscriber filter and ordered result coverage.
It rejects mismatched or incomplete selections before receipt reads. Older
status files without snapshots cannot be resumed; run recovery-status once
without --resume to create a new resumable status.

Counts and remaining are recomputed from the rows; saved aggregate values
are ignored. Remaining is the number of not_observed or read_error items.
Complete is cumulative observation coverage across the resume chain, without
a validation/read error or interruption in this call. It does not prove
current delivery completion, complete recipient-page coverage, or that an
unknown replay succeeded. Unknown saved attempts stay unknown.

Preserved observations are historical samples, not a fresh poll. To reread
them with a comparison, use --refresh; a call without --resume also starts a fresh observation. Source ownership is checked again;
Node/Python retain their bounded one-page validation refresh of the full
publication selection, without replacing copied observations. Every input,
including the saved status file, retains the 64 MiB CLI limit. Page budgets
and the overall timeout also apply. Partial failures retain output, and resume
never submits recovery or follows saved URLs.

~~~go
next, err := client.ResumeCommitRecoveryStatus(ctx, plan, applied, previous, 50, 5)
~~~

~~~ts
const next = await getCommitRecoveryStatus(source, plan, applied, {
  resume: previous, limit: 50, recipientPages: 5,
});
~~~

~~~python
next_status = await aget_commit_recovery_status(
    client, source, plan, applied, resume=previous, limit=50, recipient_pages=5,
)
~~~

See [ADR-939](adr/939-resumable-commit-recovery-status.md).


### Refresh saved recovery observations

Refresh validates the same plan, apply result and saved selection, then rereads
its delivery evidence. Refresh and resume cannot be supplied together.

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --refresh recovery-status.json \
  --limit 50 > recovery-refresh.json

gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --resume recovery-refresh.json \
  --limit 50 > recovery-refresh-next.json
~~~

The result sets refreshing to true and records each row's previous_observation.
Changes compare delivery status/reason, routing generation/relation,
acknowledged-execution attribution and observed execution ID/state. Each change
includes before/after values and available observation timestamps. A timestamp
update alone is not a change. Unread or failed observations do not produce a
comparison, and unread rows do not inherit old evidence.

Resume continues the refresh against the same baseline; another refresh uses
the supplied current observations as a new baseline. Counts, remaining and
changes are recomputed. Unknown saved attempts stay unknown even when delivery
advances. This remains read-only and uses the existing bounds and timeout.

~~~go
fresh, err := client.RefreshCommitRecoveryStatus(ctx, plan, applied, previous, 50, 5)
~~~

~~~ts
const fresh = await getCommitRecoveryStatus(source, plan, applied, {
  refresh: previous, limit: 50, recipientPages: 5,
});
~~~

~~~python
fresh = await aget_commit_recovery_status(
    client, source, plan, applied, refresh=previous, limit=50, recipient_pages=5,
)
~~~

See [ADR-940](adr/940-refresh-commit-recovery-status.md).


### Monitor acknowledged recovery executions

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --watch --interval 2s --timeout 10m \
  --limit 50 --recipient-pages 5 > recovery-final.json
~~~

Add --resume recovery-status.json to continue a partial saved pass, or
--refresh recovery-status.json to reread it with a comparison. An already
covered resume starts a fresh poll. The watch resumes partial passes before
refreshing a covered selection, with an interval between every bounded call.
The interval accepts 100ms..1m and defaults to 1s. The overall timeout includes
all reads and waits. Progress goes to stderr only when observed row fields or
counts change; final JSON goes to stdout even on timeout or interruption.

Monitoring stops when each queued attempt has matching acknowledged execution
evidence in a terminal state. Failed executions are terminal and still produce
a nonzero CLI exit. Unknown apply outcomes, unmatched queued executions and
missing evidence wait until timeout without becoming successful. Rejected,
skipped and not_attempted attempts have no acknowledged execution to monitor;
they remain explicit, with rejected/not_attempted yielding a nonzero exit.
Observation coverage alone is insufficient to settle queued attempts.

Read or validation failures stop the watch with partial output; resume that
output after resolving the failure. Samples are historical and not atomic.
Refresh comparisons use the preceding observation pass. No replay is submitted.

~~~go
ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
defer cancel()
final, err := client.WatchCommitRecoveryStatus(
    ctx, plan, applied, nil, false, 50, 5, 2*time.Second,
    func(status api.CommitRecoveryStatusResult) { /* inspect progress */ },
)
settled := api.CommitRecoveryStatusSettled(final)
~~~

~~~ts
const final = await watchCommitRecoveryStatus(source, plan, applied, {
  timeoutMs: 600000, intervalMs: 2000, limit: 50, recipientPages: 5,
  onProgress: status => console.error(status.counts),
});
const settled = commitRecoveryStatusSettled(final);
~~~

~~~python
final = await awatch_commit_recovery_status(
    client, source, plan, applied, timeout=600, interval=2,
    limit=50, recipient_pages=5, on_progress=lambda status: print(status["counts"]),
)
settled = commit_recovery_status_settled(final)
~~~

Go requires a context deadline. Node supports an AbortSignal; Node/Python
errors attach the last or partial result. Progress callbacks receive copies
and cannot alter polling inputs. Settled means acknowledged executions are
terminal; it does not mean all deliveries succeeded.

See [ADR-941](adr/941-watch-commit-recovery-status.md).


### Checkpoint recovery monitoring

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --watch --timeout 10m \
  --checkpoint recovery-checkpoint.json > recovery-final.json

# Restart with the same source, plan and result.
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --resume recovery-checkpoint.json \
  --watch --timeout 10m --checkpoint recovery-checkpoint.json \
  > recovery-final.json
~~~

Checkpointing requires --watch. It saves the resumable status after every
successful bounded pass and saves final partial evidence on read failure or
interruption when available. The saved selection and comparison baseline
continue through resume; unknown attempts remain unknown. A crash loses only
progress since the last completed save.

The CLI writes a private temporary file, syncs it, atomically renames it into
place and syncs the directory on non-Windows systems. The encoded checkpoint
uses the existing 64 MiB input limit. The directory must already exist.
Symlink/nonregular destinations and aliases of the plan/apply result are
rejected. The checkpoint can replace its own resume/refresh input after it is
loaded. Keep stdout in a separate file: shell redirection occurs before the
CLI can check paths. The CLI holds exclusive ownership of the canonical checkpoint path throughout
the watch. A second writer fails immediately before polling.

Saving failure stops monitoring with an explicit error and nonzero exit;
final JSON still goes to stdout. The preceding checkpoint remains intact if
replacement has not happened. A failure syncing the directory after rename
means durability is uncertain and also stops the watch. Validation failures
before meaningful observations do not overwrite an existing checkpoint.
An abrupt kill can leave a private temporary file; resume only the named
checkpoint. Files remain editable local observations, not signed evidence.

See [ADR-942](adr/942-commit-recovery-watch-checkpoints.md).


Checkpoint ownership uses a private persistent sidecar named PATH.lock.
Directory aliases resolve to the same ownership path. An existing sidecar is
normal after exit or a crash: the OS releases the lock automatically, so the
next watch can acquire it. Do not delete or replace the sidecar while a watch
is running. If its identity changes, saving stops with an explicit error.
Lock sidecars cannot alias input files, the checkpoint or stdout; symlinks,
nonregular files and nonprivate permissions on non-Windows systems are rejected.

These are advisory OS locks for cooperating Gregale processes. Use a filesystem
with supported locking and atomic rename semantics. Processes that bypass the
lock are not coordinated. See [ADR-943](adr/943-exclusive-commit-recovery-checkpoint-ownership.md).


### Retry transient monitoring reads

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --watch --read-retries 5 --timeout 10m \
  --checkpoint recovery-checkpoint.json > recovery-final.json
~~~

Automatic retries are opt-in and accept 0..20 additional observation passes in total
across the watch. The default remains zero. Network failures and HTTP 429,
500, 502, 503 and 504 can retry; authentication, validation, malformed evidence
and other failures stop immediately. Every failure in a partial pass must be
transient. Unread rows remain ahead of failed reads when the watch resumes.

Backoff starts at the polling interval, doubles per consumed retry and caps
at one minute. Available Retry-After seconds/date values set a longer minimum
wait. The overall timeout includes retry waits, so no request is sent early
when Retry-After exceeds the remaining time. Retryable partial results reach
progress callbacks and checkpoints before waiting; exhausted retries retain
partial output. Checkpoint/callback failures are never retried. Unknown replay
outcomes remain unknown and no replay is submitted.

~~~go
final, err := client.WatchCommitRecoveryStatusWithRetry(
    ctx, plan, applied, nil, false, 50, 5, time.Second, onProgress, 5,
)
~~~

~~~ts
const final = await watchCommitRecoveryStatus(source, plan, applied, {
  readRetries: 5, timeoutMs: 600000, onProgress,
});
~~~

~~~python
final = await awatch_commit_recovery_status(
    client, source, plan, applied, read_retries=5, timeout=600, on_progress=on_progress,
)
~~~

SDK retries honor Retry-After when retained by the HTTP error. Node Client
preserves headers on typed errors; direct generated service calls can use
only available body metadata. Python default detailed responses preserve
headers, while raised generated UnexpectedStatus errors retain only status.
See [ADR-944](adr/944-transient-commit-recovery-watch-read-retries.md).


### Structured monitoring outcomes

Watch results and attached SDK error results include a monitoring object:

~~~json
{
  "monitoring": {
    "stop_reason": "timeout",
    "passes": 8,
    "retries_consumed": 2,
    "elapsed_ms": 120000,
    "acknowledged_executions_terminal": false
  }
}
~~~

Progress callbacks and intermediate checkpoints report running. Final results
report settled, timeout, cancelled, read_failure, retry_exhausted,
validation_failure, callback_failure (Node/Python) or checkpoint_failure (CLI).
Authentication failures keep their original errors and report read_failure.
SDK option errors rejected before the watch starts can remain ordinary
validation exceptions without a status result.

Passes includes every started bounded observation call, even failed calls.
Retries_consumed counts authorized retry waits against this invocation's total
budget. Elapsed_ms measures watch time, including reads, callbacks and waits;
CLI final saving/output occurs afterward. Restarting from a checkpoint starts
new monitoring counters. Saved counters never grant additional retry budget
or prove delivery completion.

Acknowledged_executions_terminal uses the conservative watch settlement rule:
coverage must be complete, queued attempts need matching terminal evidence,
and unknown/read-error attempts prevent settlement. When no queued attempt
exists, the flag can be true; rejected/skipped/not_attempted outcomes remain
explicit. A terminal failed execution is not a successful delivery. Continue
checking row statuses and attempts as well as the monitoring stop reason.
The existing complete field still describes observation coverage only.

Final successful checkpoint saves include the final summary. If saving fails,
stdout reports checkpoint_failure; the checkpoint may still contain the last
running snapshot. Plain status reads/resume/refresh omit monitoring metadata.
See [ADR-945](adr/945-structured-commit-recovery-monitoring-outcomes.md).


### Require recent recovery observations

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --resume recovery-checkpoint.json \
  --watch --max-observation-age 2m --timeout 10m \
  --checkpoint recovery-checkpoint.json > recovery-final.json
~~~

The optional age limit accepts 100ms..24h. Zero/default disables it. With the
policy enabled, monitoring cannot settle using stale, missing-timestamp or
future-timestamp evidence. Unread rows and failed reads keep priority; other
rows requiring a reread follow, with stale observations ordered oldest first.
Reads remain bounded and output retains the original selection order. Complete
coverage with stale evidence triggers selective resume rather than restarting
a refresh from the first item. Rereads preserve the comparison baseline and
clear old current evidence before reading. Unknown attempts remain unknown.

Monitoring includes max_observation_age_ms and freshness counts:

~~~json
{
  "freshness": {
    "fresh": 45,
    "stale": 3,
    "missing_timestamp": 1,
    "future_timestamp": 0,
    "unobserved": 1
  }
}
~~~

These five counts classify current rows; failed reads are unobserved even if
a partial timestamp exists. A timestamp exactly at the age limit is fresh.
Malformed saved timestamps still fail validation. Freshness does not imply
delivery success. Complete remains coverage, while the monitoring terminal
flag also requires every row to be fresh when the policy is enabled.

~~~go
final, err := client.WatchCommitRecoveryStatusWithOptions(
    ctx, plan, applied, &previous, false, 50, 5, time.Second, onProgress,
    api.CommitRecoveryWatchOptions{ReadRetries: 5, MaxObservationAge: 2*time.Minute},
)
~~~

~~~ts
const final = await watchCommitRecoveryStatus(source, plan, applied, {
  resume: previous, maxObservationAgeMs: 120000, timeoutMs: 600000,
});
~~~

~~~python
final = await awatch_commit_recovery_status(
    client, source, plan, applied, resume=previous,
    max_observation_age=120, timeout=600,
)
~~~

Node/Python status-read helpers also accept the age option for selective
bounded rereads. Checkpoints retain the freshness summary, but restarting
requires the desired age option again; saved counts and policy metadata are
historical and never trusted for scheduling. Evidence may age while saved or
after return. Very small age limits with large selections can prevent fresh
completion and reach the overall timeout. Client wall-clock skew can produce
future timestamps; observations are not an atomic snapshot.

See [ADR-946](adr/946-freshness-aware-commit-recovery-monitoring.md).


### Diagnose recovery delivery blockers

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --watch --diagnostics --timeout 10m
~~~

Optional `--diagnostics` adds a separate object to observed rows. A matched
recipient reports its `waiting_reason`, ordering blocker, subscription/circuit
controls, recorded capacity scope, and `next_attempt_at` when present. Diagnostics
carry their own `observed_at`; they describe a current routing sample rather than
proving which replay execution caused progress.

Each pass performs at most 50 exact-recipient lookups, matching event source,
ID, acceptance timestamp and subscription. There is no subscription-page scan.
`coverage: exact_recipient` identifies the new lookup. `available` includes current
routing blocker evidence; `not_waiting` requires retained enqueued, filtered or
failed routing state and does **not** imply handler completion or successful
recovery. `acceptance_replaced` means the event identity now belongs to another
acceptance, whose blocker is not attached. `unavailable` includes a reason such
as `receipt_unavailable`, `snapshot_unavailable`, `recipient_unavailable`, or
`routing_evidence_unavailable`. Missing/foreign/expired receipts never prove
completion. `read_error` and `budget_exhausted` make incomplete diagnostics explicit.
Diagnostic errors do not change receipt status, replay attribution, settlement
or the receipt retry budget. Older servers without the endpoint produce
`read_error`; diagnostics do not silently fall back to pagination.
A recorded `next_attempt_at` is a scheduled retry time, not a guarantee of
eligibility while ordering, subscription or capacity controls still block work.
The overall timeout still applies. With diagnostics enabled, resume carries
validated samples and scheduling progress; retained samples keep their original
timestamps and are marked `retained: true`. Without diagnostics, saved diagnostic
fields are discarded. Explicit refresh starts a new diagnostic coverage cycle.

Go watches use `CommitRecoveryWatchOptions{Diagnostics: true}`; ordinary callers
can enrich a freshly validated result with `DiagnoseCommitRecoveryStatus`.
Node/Python status and watch helpers accept `diagnostics: true` / `diagnostics=True`.
Standalone SDK helpers are `diagnoseCommitRecoveryStatus(status, signal)` and
`adiagnose_commit_recovery_status(client, status)`; supply cancellation/timeouts.


### Resume diagnostic coverage across large selections

~~~sh
gregale commit recovery-status SOURCE --plan recovery-plan.json \
  --result recovery-result.json --resume recovery-checkpoint.json \
  --watch --diagnostics --checkpoint recovery-checkpoint.json --timeout 10m
~~~

Diagnostics still make at most 50 exact lookups per pass. Unread queryable rows
come first, then failed diagnostic reads, then cyclic refresh of earlier samples.
Separate cursors rotate each group. When failures and successful samples compete,
one refresh slot is reserved so persistent failures cannot starve healthy samples.
Receipt rows without usable observations are deferred; unsupported recipients
are excluded. Selection order in JSON stays fixed.

The separate `diagnostics_progress` object reports `target_count`, `remaining`,
`deferred`, `retained`, per-status `counts`, `complete`, and this pass's `reads`.
It also carries resume cursors. Counts are rebuilt from validated row samples;
saved aggregate values are ignored. `complete` means sampled coverage, including
explicit unavailable/replaced evidence, rather than successful delivery or the
absence of blockers. Retained samples describe earlier observations; consult
their original `observed_at` before treating them as current. Deferred rows may
still have historical samples.

Progress appears in callbacks and watch checkpoints. Diagnostic completeness
does not delay settlement or affect delivery counts, freshness, replay attribution
or exit status. To continue coverage after a settled watch, run ordinary
`recovery-status --resume ... --diagnostics` repeatedly, saving its JSON for the
next resume. Use a separate output path and replace the input only after success.

Go callers can use `ResumeCommitRecoveryDiagnostics(ctx, status, previous)` after
validating the current recovery selection. Node callers pass previous status as
the third argument to `diagnoseCommitRecoveryStatus(status, signal, previous)`;
Python callers use `adiagnose_commit_recovery_status(client, status, previous=previous)`.
Status/watch helpers wire this automatically when diagnostics and resume are enabled.

Diagnostic passes now submit up to 50 exact recipients through the read-only
`POST /v1/events/recipient-blockers` endpoint. Each ordered item keeps its own
acceptance guard, snapshot and result or error. The batch shares a five-second
read deadline; later items may report `read_timeout` and rotate into retries.
Coverage reads count recipient attempts, rather than HTTP requests. Batch
coverage does not prove delivery success or handler completion.

### Limit diagnostic sample age

Use `--diagnostics --max-diagnostic-age 30s` with ordinary, resumed or watched
recovery status. The default `0` disables the age policy; enabled values are
100ms–24h. Stale samples refresh ahead of fresh samples within the covered
refresh pool, after unread and failed-read scheduling, under the same
50-recipient batch cap.

`diagnostics_progress.freshness` reports `fresh`, `stale` and `unread` counts,
with `freshness_checked_at`, `max_diagnostic_age_ms` and `fresh_complete`.
Future timestamps count as stale when the policy is enabled. Disabled age
policy counts every valid sample as fresh. Historical `complete` still means
sample coverage; diagnostic freshness never gates delivery settlement or
command success. A resumed checkpoint uses the current invocation's age policy
and recomputes counts from the original sample timestamps. An age limit shorter
than the monitoring interval may leave samples stale on every pass.

### Track routing blocker changes

With `--diagnostics`, repeated or resumed samples include `comparison`, a compact
`previous_observation`, and field-level `changes` with `field`, `previous` and
`current` values. The current sample's `observed_at` and the previous observation's
`observed_at` identify the comparison interval. For example, a waiting reason can
change from `subscription_paused` to `ready`, or routing state from `pending` to
`enqueued`. Routing readiness and enqueueing do not prove handler completion.

`diagnostics_progress.compared` and `changed` count newly sampled comparable and
changed recipients in that pass. Watch summaries print each new changed pair.
Retained comparisons survive checkpoints with their original timestamps and do
not count as new changes. Saved change arrays are recomputed from validated
observations on resume. Only the latest pair is retained, not an unbounded history.

Comparison requires the same exact recipient and acceptance guard, confirmed
routing evidence, and strictly increasing timestamps. `baseline_unavailable`,
`evidence_unavailable` and `non_monotonic_timestamp` emit no changes. An evidence
gap resets the baseline. Waiting, capacity, target and ordering fields are compared
only between two available observations; ages, counters, leases and links are
excluded. Blocker changes remain separate from receipt execution changes,
replay attribution, coverage, freshness and delivery settlement.

An explicit diagnostic refresh begins a new baseline, consistent with existing refresh behavior; ordinary resume preserves the latest validated comparison.

### Watch until diagnostic coverage completes

~~~sh
gregale commit recovery-status SOURCE --plan plan.json --result apply.json \
  --watch-diagnostics --max-diagnostic-age 30s --timeout 10m \
  --checkpoint diagnostic-status.json
~~~

`--watch-diagnostics` enables both watch and diagnostics. It uses the existing
50-recipient batch rotation until every relevant recipient has a valid sample
and meets the configured diagnostic age policy, independently of execution
settlement. Resume using `--resume diagnostic-status.json` and the same watch
flag; the current invocation supplies the age policy. Explicit refresh starts
new diagnostic coverage. Checkpoint ownership and atomic saving remain in effect.

Successful completion reports `monitoring.watch_mode=diagnostic_coverage`,
`stop_reason=diagnostic_coverage_complete`, and
`diagnostic_coverage_complete=true`. CLI exit zero means diagnostic coverage
completed; failed or unresolved deliveries remain visible and do not change that
inspection result. `acknowledged_executions_terminal` remains independent.
Normal `--watch` still stops on recovery settlement and keeps its existing exit
semantics.

Freshness is rechecked after progress callbacks and recorded at the completion
decision. With age disabled, valid samples can be historical. `unavailable` and
`acceptance_replaced` responses count as observed coverage, not routing readiness;
unsupported recipients are excluded. Empty relevant selections complete normally.
Small age limits may never allow the full selection to be fresh at once.

Batch transport/read failures use `--read-retries` and existing retry limits;
permanent failures stop with `read_failure`, and exhausted transient retries with
`retry_exhausted`. Individual diagnostic item failures keep rotating until the
overall timeout. Timeout or interruption preserves current partial coverage for
resume. No monitoring mode submits recovery actions.

### Estimate diagnostic coverage time

Diagnostic watch summaries include `monitoring.diagnostic_estimate` with remaining
recipients, successful samples per second/pass, and estimated remaining passes
and milliseconds for **initial sampling**. Both settlement watch with diagnostics
and `--watch-diagnostics` expose the estimate. It is advisory and does not change
completion, timeout, retry or delivery behavior.

States are `insufficient_data`, `awaiting_receipts`, `estimated` and `covered`.
`covered` means initial sampling is complete; it does not mean all samples are
fresh. Unread recipients needing receipt evidence are reported as deferred and
prevent time forecasts. Forecasts also remain absent until there is successful
sampling and elapsed timing. Valid unavailable/replaced answers count as samples,
while retained snapshots do not count as new responses.

The estimator uses actual diagnostic reads, monotonic watch elapsed time,
average samples per pass and the next polling/retry delay. `estimated_refresh_ms`
models the span needed to refresh all targets. If this exceeds an enabled age
limit, `refresh_exceeds_max_age` becomes true and CLI emits an advisory warning.
This does not prove that fresh coverage is impossible. Persistent recipient
failures or changing performance can invalidate a forecast.

Checkpoints retain estimates for inspection, but resumed watches start new timing
measurements. Coverage, sample timestamps and cursors still resume normally.
The estimate's deferred count includes only unread targets; diagnostic progress's
deferred count can also include valid retained samples. CLI avoids printing
changing rate/ETA values on every tick, while progress callbacks and JSON expose
updated estimates.

### Inspect selected consumers

Diagnostic progress includes `consumers`, sorted by subscription ID. Each summary counts relevant selected recipients, fresh/stale/unread coverage, validated sample statuses, fresh and stale waiting reasons separately, and newly compared/changed recipients in this pass. Waiting reasons expose paused subscriptions, circuits, ordering, capacity and other routing conditions without additional requests. Unsupported recipients are excluded. A valid unavailable or replaced answer counts as sampled coverage but contributes no waiting reason.

`oldest_waiting` identifies the earliest accepted event among validated available samples, with its exact query, observation timestamp, freshness and waiting reason. Acceptance time is not the time a blocker started. Stale waiting reasons describe historical samples; these summaries are neither account-wide backlog totals nor delivery success. Saved totals are ignored and recomputed from validated rows at the progress freshness check time. CLI watch output includes these summaries when consumer evidence changes, without timestamp-only polling updates.

### Detect persistent sampled blockers

Enable watch stall detection with `--stall-threshold 30s --watch --diagnostics --max-diagnostic-age 10s` (both duration policies accept 100ms–24h; zero disables detection). Go watch options use `StallThreshold`, Node uses `stallThresholdMs`, and Python uses `stall_threshold` in seconds. Watch results include `monitoring.stall_threshold_ms` and `monitoring.consumer_stalls`, grouped by selected subscription. CLI alerts once per confirmed blocker episode and can alert again after confirmation breaks. Detection does not change watch completion, timeouts, retries or delivery actions. A coverage watch can complete before the stall threshold is reached.

Each consumer has recipient counts for `insufficient_evidence`, `not_blocked`, `observing` and `stalled`, plus overlapping indicators `routing_changed` and `advancing` for the most recent confirmed pair. Advancing specifically means pending to processing, or available to enqueued; it does not prove handler success. Other routing changes can improve or worsen conditions. Blocker details contain the exact query, reason, first/last sample timestamps, sample count and `monitored_ms`: the monotonic span between distinct samples in this invocation, never the original blocker duration.

At least two strictly increasing fresh samples of the same routing observation must span the threshold. Pauses, circuit/rate limits, ordering, capacity and retry backoff are eligible; ready and processing indicators are not blockers. Changes in routing state, target availability, capacity scope or exact ordering blocker reset the episode. Stale, future, unavailable, replaced, invalid and non-monotonic evidence interrupt confirmation; a gap exceeding the age limit also resets it. Retained samples cannot start an episode or extend its duration. Resume starts a new tracker and ignores checkpoint stall aggregates. Counts describe sampled selected recipients, not account-wide stalled consumers.

### Consume blocker incident transitions

Stall-enabled watch results include `monitoring.blocker_incidents` (currently confirmed incidents) and `monitoring.blocker_transitions` (events from the current pass). An incident opens when fresh samples reach the stall threshold. Its opaque ID stays fixed during that episode within this watch. Each transition has an increasing invocation-local sequence, incident ID, exact guarded query, original waiting reason, notification timestamp and optional compact routing evidence. Deduplicate repeated snapshots by incident ID and sequence. A newly confirmed episode receives a new ID. Without durable alerts, resume starts a new invocation and does not adopt saved incidents or replay their transitions.

Transitions are `opened` / `persistent_blocker`, `resolved` / `routing_not_blocked`, or `confirmation_lost`. Resolution requires a newly sampled, strictly later, fresh observation without an evidence gap: either retained routing has ended (`not_waiting`) or the sampled waiting reason is no longer an eligible blocker. This includes terminal routing failures/filters and never proves handler success. Changed blockers or episode resets report `confirmation_lost` / `episode_reset`; stale, unavailable, invalid or replaced evidence reports `evidence_unavailable`, excessive sampling gaps report `sample_gap`, and missing selected evidence reports `selection_unavailable`. Missing evidence never implies resolution.

Add `--alerts-json` to a stall-enabled CLI watch to emit one JSON transition per line on stderr and suppress routine watch progress text. The final status JSON remains on stdout; command errors retain their existing error reporting. The CLI deduplicates callback/final snapshot events and emits transitions discovered by final freshness checks. Without this flag, lifecycle alerts use readable text. Watch completion and deadlines are unchanged: a stopped watch may still have active confirmed incidents. Active state and current-pass events are bounded by the selected recipients; these results are not a durable incident history or a delivery success signal.

### Resume pending incident notifications

A stall-enabled CLI watch with `--checkpoint PATH` now enables a durable alert outbox. SDK watch options enable it with Go `DurableAlerts: true`, Node `durableAlerts: true`, or Python `durable_alerts=True`. SDK callers must persist progress and final snapshots themselves, before handing alerts to a consumer. `monitoring.alert_outbox` contains version 1, a notification session, its latest sequence, ordered `pending` transitions and saved `active` incidents. Consume `pending` when durable mode is enabled; `blocker_transitions` remains a per-pass view.

Resume validates saved alerts against the same frozen publication/acceptance/subscription selection and reviewed attempts, checks episode lineage and bounded queue shape, and preserves pending IDs, sequences and the notification session. This extends the invocation-only lifecycle behavior above for durable notification streams. Previously active episodes emit `confirmation_lost` with reason `watch_resumed`; saved samples never restore live confirmation. New episodes require fresh samples and receive new IDs in the continued stream. Historical pending alerts remain historical notifications even when current evidence has disappeared.

Printing or writing an alert is not acknowledgement from the consumer. After processing an alert, acknowledge its exact incident ID and transition sequence. CLI accepts an array such as `[{"incident_id":"0123456789abcdef0123456789abcdef-1","sequence":1}]` via `--ack-alerts PATH` with checkpoint and resume/refresh. The acknowledgement file is protected from checkpoint overwrite. SDKs accept initial `AlertAcknowledgements` / `alertAcknowledgements` / `alert_acknowledgements` and synchronous per-pass `OnAlertAcknowledgements` / `onAlertAcknowledgements` / `on_alert_acknowledgements` callbacks that return acknowledgement arrays after progress. Acknowledgements validate as a whole; duplicate pairs within a batch, wrong-stream IDs, future sequences and conflicting IDs for pending sequences fail without removing entries. Repeating an acknowledgement for an already removed sequence in this stream is an idempotent no-op. Callbacks should return promptly and acknowledge only completed consumer processing. Persist the next/final snapshot to commit removals; a crash before that save can replay acknowledged work.

The CLI saves each snapshot before emitting alerts, including final-check transitions, and re-emits unacknowledged pending entries once per resumed invocation. This is at-least-once delivery of checkpointed notifications, not exactly-once processing; deduplicate by incident ID and sequence, and apply only transitions newer than the last sequence processed for that incident. Partial acknowledgements can replay an older transition after a newer one was processed. Resume the latest snapshot of a single checkpoint stream; SDK callers must serialize checkpoint ownership and writes. Notifications that were never checkpointed have no durability guarantee. Consumer acknowledgements are explicit; stderr success does not remove pending alerts.

The queue is bounded at 1,024 slots, including a reserved closing transition for every active incident. If a new opening cannot reserve both slots, monitoring stops with `alert_queue_full` and retains queued notifications and the sampled status. Acknowledge pending work and resume. No queued transition is evicted. Notification sequences are bounded to the exact integer range shared by the SDKs. Invalid saved state/acknowledgements are rejected and do not overwrite the CLI checkpoint. Non-durable watches retain the previous per-pass behavior. Delivery mutations, receipt budgets and ordinary watch completion are unchanged.

### Manage saved alerts without restarting a watch

These commands work locally without login or delivery polling:

```sh
gregale commit recovery-alerts --checkpoint watch.json
gregale commit recovery-alerts --checkpoint watch.json --replay
gregale commit recovery-alerts --checkpoint watch.json --ack-alerts processed.json
```

Inspection returns the historical outbox, `pending_count`, `reserved_closing_slots`,
`remaining_slots`, and `capacity`. Replay writes pending transitions in sequence
order as JSON lines on stdout, preserving IDs and timestamps. Printing never
acknowledges an alert. Deduplicate downstream by `incident_id` and `sequence`.
The acknowledgement file is an array of exact pairs, for example
`[{"incident_id":"<saved-incident-id>","sequence":1}]`. The entire batch is
validated before any entries are removed; repeated previously removed pairs in
the same stream are safe. Acknowledgements atomically update only the pending
queue. Active reservations and saved evidence remain historical and unchanged.
All operations hold checkpoint ownership and fail while another watch owns it.
Local validation checks saved queue/selection consistency, not fresh delivery
state or independent correspondence with an original reviewed plan.

### Subscription delivery health

```sh
gregale events subscription-delivery-health my-app <subscription-id> --window 15m
gregale --json events subscription-delivery-health my-app <subscription-id> --window 15m
```

The read-only `GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-health`
combines routing health, retained execution health, and up to 50 oldest pending
application recipients. Routing pending/processing counts cover the live backlog.
Execution retrying and dead-letter counts include retained handler replays, are
bounded, and are historically incomplete; routing success alone is admission.
`latest_successful_completion_at` is the latest valid success among retained
executions, not an all-time watermark. It is absent when no valid retained
completion is available. The metrics window does not restrict current execution
state counts or this timestamp.

`pending_sample.waiting_reasons` counts sampled pending recipients only. Inspect
`truncated` before treating those counts as complete. The first recipient identifies
the oldest pending event in that read. Paused state, circuit state, and configured
rate describe subscription controls; sampled recipients show actual waiting reasons,
including capacity and ordering blockers. No overall healthy verdict is inferred.

Each component is an independent read with its own `observed_at`; top-level
`started_at` and `observed_at` bound assembly. Concurrent progress may make their
counts differ. The response is not cached, uses the existing bounded subscription
read deadline, and fails if any component read fails. Supported windows are
`5m`, `15m`, `1h`, `6h`, and `24h` (default `5m`).

### Watch subscription delivery health thresholds

```sh
gregale events subscription-delivery-health my-app <subscription-id> --watch \
  --oldest-pending-threshold 5m --retrying-threshold 10 \
  --dead-letters-threshold 1 --pause-threshold 10m \
  --circuit-threshold 1m --capacity-threshold 1m \
  --interval 5s --max-age 30s --timeout 15m --alerts-json
```

Enable at least one threshold. Zero disables it; count thresholds must be positive
when enabled, and duration thresholds range from 100ms to 24h. Watches emit one
`opened` per newly confirmed breach, `resolved` only on fresh usable evidence below
the threshold, and `confirmation_lost` when confirmation becomes unavailable.
Inclusive comparisons mean a count equal to its threshold breaches. Deduplicate
by `incident_id` and `sequence`; a closing transition preserves its opening ID.
Each alert includes the bound application and subscription IDs. JSON alerts go
to stderr. Global `--json` writes the final watch result to stdout.
The watch exits nonzero on timeout, cancellation, read failure, or callback failure,
preserving its last observed health and condition state in the final result.

Pending acceptance age and pause duration use server observations. Circuit and
capacity durations measure elapsed time between consecutive fresh samples in this
invocation and need at least two advancing samples. Circuit restriction includes
open, half-open, and draining controls. Capacity monitoring uses complete
`capacity_waits` aggregates independently of
pending-sample truncation. Missing, incomplete, stale, or invalid aggregates cannot
clear an alert.
These are sampled subscription conditions, not continuous measurements or the
original age of a specific blocker.

Execution thresholds skip truncated coverage, missing roots, unknown executions,
and absent retained roots. Unknown/stale/future/non-advancing evidence resets local
confirmation rather than reporting healthy. Usable execution counts still cover
retained executions, not all-time event totals. Per-component `--max-age` (default
30s) also limits sample gaps; choose it larger than your polling interval (default
5s). Interval bounds are 100ms–1m; timeout defaults to 2m and is bounded at 24h.

Alerts are local to this watch, reset on restart, and are not checkpointed. Use
existing server alert rules for durable webhooks on supported pending-age,
pause-duration, and dead-letter metrics. Watch-specific options require `--watch`;
monitoring does not replay deliveries or change subscription controls.


### Complete subscription capacity waits

Delivery health includes `capacity_waits`: complete counts by `consumer`, `app`,
and `account` scope, the matching `pending_recipients` total, `coverage`, `complete`,
`app_id`, `subscription_id`, and its own `observed_at`. These counts cover all
retained pending application recipients, including backfills, in one backlog
snapshot and are independent of the 50-event sample. They count effective waiting
reasons after pause, circuit, ordering, and other classification precedence.
The existing backlog `capacity_waiting_recipients` field continues to count recorded
capacity scopes and can therefore differ from these effective-reason counts.

The capacity watch can now confirm or clear conditions on large backlogs. It uses
fresh complete aggregates, never infers zero from a missing field or truncated
sample, and validates scope counts against their own pending total. Older servers
without `capacity_waits` produce unknown capacity confirmation. Other summary
components remain independent reads; use the aggregate's timestamp when assessing
freshness. Scope counters are available in the backlog consumer API as
`capacity_consumer_waiting_recipients`, `capacity_app_waiting_recipients`, and
`capacity_account_waiting_recipients`, with the backlog's existing filters.

### Complete pending waiting-reason counts

Subscription delivery health now includes optional `waiting_reasons` with `counts`,
`pending_recipients`, `complete`, `coverage`, app/subscription IDs, `observed_at`, and
`window_at` (the backlog acceptance cutoff). The CLI displays this aggregate in
`events subscription-delivery-health`; JSON output retains the full dictionary.

Counts cover every matching retained pending application recipient independently
of the 50-event sample. They include pause and rate limiting, circuit restrictions,
ordering, capacity scopes, retry backoff, receipt processing, and ready classification.
All known reasons retain zero entries; scope-inapplicable reasons also remain zero.
Each recipient contributes to one effective reason after the existing classification
precedence. A complete dictionary sums exactly to its own pending total; do not
compare it to an independently read routing total as if they were one snapshot.
Ready does not prove target availability, routing admission, or handler success.

Missing aggregates from older servers are unavailable, not zero. Use completeness,
coverage, and observation time before concluding anything about the current backlog.
The backlog consumer API also exposes `pending_waiting_reasons` for each matching
consumer group, respecting its existing filters and excluding processing recipients.
Existing capacity alerts continue to use their complete capacity aggregates.

Waiting-reason health alerts use complete pending aggregates, even when the event sample is truncated:

```sh
gregale events subscription-delivery-health my-app SUBSCRIPTION_ID --watch \
  --waiting-reason-count ordering_blocked=10 \
  --waiting-reason-count retry_backoff=100 \
  --waiting-reason-duration subscription_rate_limited=5m --timeout 15m --alerts-json
```

Both flags are repeatable for different known reason keys; duplicates within one flag kind are rejected. Count thresholds are inclusive. Duration thresholds measure consecutive fresh local observations with a nonzero reason count, requiring at least two observations; they do not track the same recipients. Missing, incomplete, stale or inconsistent aggregates cause confirmation loss rather than clearing an alert. Notifications remain invocation-local and are emitted once per transition on stderr. See ADR-964.

To preserve delivery-health alerts across restarts, add `--checkpoint health-watch.json`. Resume with the same app, subscription, window and thresholds plus `--resume`:

```sh
gregale events subscription-delivery-health my-app SUBSCRIPTION_ID --watch \
  --waiting-reason-count ordering_blocked=10 --checkpoint health-watch.json --alerts-json
# After the first watch exits:
gregale events subscription-delivery-health my-app SUBSCRIPTION_ID --watch \
  --waiting-reason-count ordering_blocked=10 --checkpoint health-watch.json --resume --alerts-json
```

Saved incidents resume as unknown until fresh evidence confirms or clears them; qualifying evidence preserves their IDs. Duration measurements restart. The watch atomically persists progress before printing notifications. The queue reserves closing slots for active incidents, stops new openings when full, and never automatically acknowledges printed alerts. While the watch is stopped, inspect, replay or acknowledge its queue without logging in:

```sh
gregale events delivery-health-alerts --checkpoint health-watch.json
gregale events delivery-health-alerts --checkpoint health-watch.json --replay
# processed.json contains [{"incident_id":"SESSION-OPENING_SEQUENCE","sequence":TRANSITION_SEQUENCE}]
gregale events delivery-health-alerts --checkpoint health-watch.json --ack-alerts processed.json
```

Replay emits historical JSON lines and leaves the queue unchanged. Acknowledge exact pending transitions only after processing them. Repeated exact pairs are idempotent while retained in the bounded acknowledgement history; unknown pairs and older evicted pairs are rejected. Deduplicate externally by session and sequence. Watches and queue operations share an exclusive private sidecar lock; keep the `.lock` file in place. See ADR-965.

To acknowledge alerts while a watch keeps running, add a separate inbox:

```sh
gregale events subscription-delivery-health my-app SUBSCRIPTION_ID --watch \
  --waiting-reason-count ordering_blocked=10 --checkpoint health-watch.json \
  --ack-inbox processed-inbox.json --alerts-json
```

After processing notifications, your consumer atomically replaces `processed-inbox.json` with a JSON array of exact `incident_id`/`sequence` pairs. The watch reads the file between health observations, saves removals before adopting them, and continues polling. Use `--resume` with the same flags after a restart. A missing inbox is allowed; clear a batch by atomically replacing it with `[]`. The watch never deletes or edits the inbox.

Repeated pairs are safe within the last 1,024 removed transitions, persisted in the checkpoint. Use recent bounded batches; this is not a permanent cumulative acknowledgement log. Malformed files, foreign or unknown pairs, and pairs evicted from retry history stop the watch without removing pending notifications. Inbox paths must be regular files separate from the checkpoint and its lock. Acknowledgement frees notification slots but does not clear incidents or release their reserved closing slots. See ADR-966.
