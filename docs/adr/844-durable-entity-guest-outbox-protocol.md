# ADR-844 · Gated durable entity guest outbox protocol

- **Status:** implemented locally; automated and native/live-provider verification pending
- **Date:** 2026-10-09
- **Decision:** Introduce separately gated guest protocol v2 for bounded registered-app-webhook intents. Advertise central transition/batch limits, validate returned intents and current destination/scope admission before engine commit, retain v1 unchanged, and supply pure Go/Node SDK helpers and a reservation/confirmation example.
- **Why:** ADR-829 atomically commits outgoing intents with entity state and ADR-843 supplies recoverable transport handoff. Applications still cannot describe outgoing work through guest protocol v1.
- **Consequences:** Preview handlers can return state, result, alarm and outgoing intents together. Object storage remains authoritative for state/pending work; SQL transport acceptance/delivery metadata stays separate. Guest success, entity publication and receiver delivery are distinct outcomes. All preview gates stay disabled here.
- **Rejected alternatives:** Accepting outbox in v1 silently changes older deployments. Sending from a handler can escape rejected state publication. Arbitrary URLs/credentials bypass registered destinations. Guest-selected versions bypass platform gating. Duplicated SDK quotas drift from central admission.

## Protocol and gates

The default-off `FAAS_DURABLE_ENTITY_OUTBOX_HANDLERS_ENABLED=1` requires the
invocation preview and `FAAS_DURABLE_ENTITY_OUTBOX_ENABLED=1`. Invalid
combinations fail startup before provider access. The relay still requires
deduplicating transport support and private listing/probe deletion. Enabling
the handler gate selects v2 for **all explicitly allowlisted apps**, for both
`invoke` and `alarm` events; prepare their handlers before enabling it.

Without both flags dispatch stays at v1, which rejects any `outbox` field,
including `[]` and `null`. V2 adds `limits` to the otherwise unchanged guest
request: `transition_bytes`, `identity_bytes`, `outbox_messages`,
`outbox_payload_bytes`, and `outbox_bytes`, taken from `pkg/api/limits.go`.
These advertise bounds, not available capacity or pending work. Requests contain
verified entity scope, business state, request identity/payload and deployment
identity, never claims or bucket credentials. The platform uses the version it
selected for the request to decode the response; a guest cannot upgrade itself.

V2 accepts `data`, `result`, optional `alarm_at`, and optional `outbox`.
Each intent contains `webhook_id`, `event_type`, and JSON `payload`. The webhook
ID must be a canonical nonzero UUID for an enabled registered app webhook.
URLs, signing secrets, guest message IDs and unknown fields are rejected.
Omitted/empty outgoing batches append nothing and never clear pending work.
An omitted/null alarm clears it. Old pure response bodies also decode under v2;
v1 request envelopes remain unchanged.

## Admission and publication

Before returning the guest transition to the engine, apid checks shape, count,
encoded payload expansion and encoded intent bytes. For a nonempty batch it
reloads account/app admission, abuse/plan holds, allowlist/gates, immutable
environment identity and any selected active tenant. Each distinct destination
must be an enabled app webhook owned by the exact account/app. Account/tenant
webhook scopes fail closed. Read failures are redacted. Rejection uploads no
candidate state/receipt. The engine independently enforces complete pending
queue, snapshot and storage-cap bounds before uploads, then publishes state,
result, receipt, alarm and messages through one ownership-fenced manifest CAS.

Admission reads cannot be atomic with bucket CAS. A destination/scope can change
after validation but before publication, leaving a committed pending intent.
ADR-843 rechecks scope at handoff and locks/checks destination/account/app with
the first atomic SQL acceptance. Work is never silently dropped or redirected.
This does not promise revocation after acceptance. Receipt replay returns already
committed results without rerunning the guest or appending messages; relay
acceptance still requires current admission.

The platform derives message IDs from full scope, committed version and ordinal.
Receiver delivery is at least once; recipients deduplicate the stable message/
delivery ID. Commit success does not imply acceptance or receiver completion.
FIFO orders acceptance, not concurrent receiver completion.

## SDK and example

Go adds `DecodeDurableEntityHandlerRequest`, typed identity/state/limits/
transition/intent types, `request.WebhookIntent`, and `request.EncodeTransition`.
Node adds equivalent decoding, intent and encoding helpers. They produce JSON
only, validate negotiated versions/bounds, and reject v1 outgoing work. Payload
checks include Go JSON HTML escaping. Handlers must stay pure and validate their
business input. Envelope parsing does not authenticate a public endpoint or
grant platform authority.

`scripts/gen-durable-entity-contract.py` generates both SDKs' path, protocol
versions and request/transition byte constants from the central table, plus
shared wire fixtures. Repository lint checks drift. Other batch limits come
from the v2 envelope. Node rejects state versions outside JavaScript's safe
integer range rather than silently rounding uint64; Go supports the full range.

`examples/durable-entity-reservations` stores one reservation per entity and
returns its confirmation intent together with state. The webhook ID comes from
app configuration. An identical reservation with a new request ID returns state
without another confirmation; a changed quantity conflicts. It neither
coordinates inventory across entities nor schedules alarms or sends during
computation. Its bundle contains two built SDK modules for an ordinary HTTP app,
with no database, permanent disk or bucket credentials.

## Verification and rollout

No PR, flags, deployment or live bucket changed here. Complete ADR-843's schema-6
upgrade and acceptance migration and qualify the relay **before** enabling v2.
Update every allowlisted guest for v2. Disabling only the handler flag stops new
guest intents while the enabled relay can drain existing work. Schema downgrade
still requires ADR-843's explicit migration and receipt preservation.

Added source tests cover shared Go/Node/platform fixtures, v1/v2 compatibility,
unknown authority fields, destination shape, encoded bounds, lost publication
acknowledgement with restart/replay/transport acceptance, full-queue rejection,
late destination/account/app/plan/abuse/allowlist/gate/tenant/environment changes,
alarm-produced intents, and the runnable example. They have **not been run**.

The testing agent must run durableentity/apid/state/operator and both SDK suites,
with pinned toolchains, plus repository lint/sqlc/contract drift checks. Node's
`test:unit` includes helper and example tests after building the SDK. Then
qualify native guest scheduling, actual receiver delivery/deduplication and
real-GCS crash/partition/restart behavior with an isolated app/bucket. Record
results before enabling the preview.
