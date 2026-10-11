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
