# Durable reservation with a confirmation intent

This locally implemented, unverified preview demonstrates one object-storage
entity per reservation. The handler returns reservation state and a registered
webhook confirmation intent together. Gregale publishes them with the result
receipt and relays the committed intent afterward. The handler never sends the
confirmation or needs a SQL database, bucket credentials or persistent disk.

## Prepare the preview

The testing agent must first qualify the relay, apply migration
`20261010010028804_entity_outbox_acceptance.sql`, and complete the schema-6 writer
upgrade in [ADR-933](../../docs/adr/933-durable-entity-outbox-relay.md). Keep the
invocation preview's private backend/bucket placement stable and its explicit
app allowlist configured. No feature is enabled by this example.

Register an enabled **app** webhook owned by this app/account using the existing
webhook API. Configure the app's `CONFIRMATION_WEBHOOK_ID` with its canonical UUID;
the app receives no receiver signing secret or URL. Enable revision pinning
(`revision_pin_ttl_seconds`, for example 3600), and register any selected project
environment as described in the [counter preview](../durable-entities/README.md).

After automated and native/live-GCS qualification, the operator may separately
configure apid with `FAAS_DURABLE_ENTITY_OUTBOX_ENABLED=1` and
`FAAS_DURABLE_ENTITY_OUTBOX_HANDLERS_ENABLED=1`. The latter selects v2 for **all
allowlisted apps**, including alarm handlers. Prepare those guests together;
the existing counter example only supports v1. Disabling just the handler flag
stops new guest intents while an enabled relay continues draining pending work.

## Package and deploy

Build the checkout SDK and package this example on the testing/deployment host:

```sh
npm --prefix sdk/node ci --ignore-scripts
npm --prefix sdk/node run build
node examples/durable-entity-reservations/bundle.mjs
```

Deploy the printed fresh directory through the usual Gregale source-deployment
flow. It contains only example code, a Node manifest and two compiled SDK
modules. It excludes environment files and credentials. The app starts with
`npm start`, listens on port 8080 and exposes `/healthz`. Its guest handler is
`POST /__gregale/entities`. Parsing that envelope does not authenticate public
requests; only Gregale's scoped invocation and fenced commit publish state.

## Invoke a reservation

Send this body to authenticated `POST /v1/apps/<app-slug>/entities/invoke`:

```json
{
  "namespace": "reservations",
  "key": "reservation:123",
  "request_id": "reserve-123",
  "payload": { "action": "reserve", "quantity": 2 }
}
```

Add `environment` and/or `platform_tenant_id` when selecting a registered project
environment or active same-account customer. Keep `request_id` and the **exact
payload bytes** when retrying an uncertain call. HTTP `Idempotency-Key` alone
does not identify entity work.

The API returns `{"status":"reserved","quantity":2}` at version 1. Repeating
the same request replays its original result without running the guest or
appending a confirmation. A new request ID for an identical reservation returns
the state without another confirmation; a different quantity conflicts. This is
a reservation record, not a transaction across inventory entities.

The intent names the configured webhook and `reservation.confirmed`. The
relay's delivery body contains verified `entity`, stable `message_id`,
`state_version`, `ordinal`, and `data` containing the reservation key/quantity.
Verify signatures and deduplicate the message/delivery ID before non-idempotent
receiver effects. Delivery is at least once. API commit success does not mean
a webhook has been accepted or received; FIFO orders acceptance, not receiver
completion.

## Bounds and recovery

V2 advertises central limits to the SDK. The engine also checks the full queue,
snapshot and retained-storage cap. Invalid/oversized outgoing work rejects the
whole state transition. Omitting `outbox` preserves pending messages; omitting
`alarm_at` clears the alarm. This example never schedules alarms. Destination or
scope changes after validation may leave committed work pending; relay admission
is rechecked rather than dropping it.

State and pending intents live in object storage. Existing transport delivery
records and permanent stable-ID acceptance receipts use SQL. Inspect exhausted
heads and retry them through the private
[operator harness](../durable-entities/README.md#internal-outbox-relay-and-operator-recovery).
Acceptance retries retain identity and do not recreate accepted deliveries after
history pruning. Receiver recovery uses the existing webhook delivery tools.

## Testing-agent handoff

New cases have not been run here. Run durableentity/apid/state and Go SDK checks,
`npm --prefix sdk/node run test:unit` (which includes this example), and repository
lint/SQL/SDK drift gates using pinned toolchains. Then qualify native guest
dispatch, GCS failure/restart recovery, actual signed receiver delivery, duplicate
handling and exhausted-head recovery before enabling either gate. See
[ADR-934](../../docs/adr/934-durable-entity-guest-outbox-protocol.md).

## Inspect durable entity metadata

The owner-only preview endpoint `GET /v1/apps/{slug}/entities/inspect` accepts
required `namespace` and `key`, plus optional `environment` and
`platform_tenant_id`. It returns the committed version, alarm status and pending
outbox metadata without running the guest or exposing state/message payloads.
It requires account `apps:read` or admin permission and preview app enablement.
Missing delivery history is `unknown`; an empty queue does not prove delivery.

## Re-arm exhausted entity work

Use `POST /v1/apps/{slug}/entities/retry` after a fresh inspection reports the
selected alarm or outbox head as exhausted. Copy its version, recovery revision
and exact alarm deadline or head ID. Recovery requires account deploy-write or
admin permission and existing execution admission; diagnostic read permission
alone does not grant retry authority. Active owners block recovery.

A successful response resets retry metadata only. Workers must be enabled to
resume processing. Committed state and message identities stay intact; terminal
receiver deliveries are not resent. After a conflict or uncertain response,
inspect again before deciding whether to submit another recovery.
