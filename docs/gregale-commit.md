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
