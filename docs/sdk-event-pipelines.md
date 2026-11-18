# Typed event-triggered pipelines (SDK preview)

Start a typed Job pipeline from an authenticated application CloudEvent. Node
and Python share payload schemas, receipt formats and PostgreSQL coordination.
This composes existing Events and Workflows APIs. Existing SDK callers and
public entry points keep their behavior; these helpers are preview-only.

Event-triggered runs add an optional `trigger` identity to the native input
envelope. Upgrade the SDKs used by backends/drivers that start or resume these
runs so their checkpoints retain that binding. Ordinary pipeline starts keep
their existing envelope and checkpoint shape. The new application-owned event
receipt table is an explicit opt-in installation, not a platform migration.

## Generate the event payload

An event contract contains `version: 1`, a CloudEvents `type`, and `data` using
the same bounded schema subset as typed Jobs. Generate TypeScript or Python
`Input` types and an `EventContract[Input]` validator from one JSON file:

```sh
gregale-event-types event-contract.json typescript event-generated.ts
gregale-event-types event-contract.json python event_generated.py
# Or, from the Python SDK:
python -m faas_sdk.preview.event_types event-contract.json python event_generated.py
```

See [the CSV event contract](../examples/sdk-csv-jobs/event-contract.json).
An event can name an immutable business record; it need not carry file locations
or sensitive business data. The event type, payload, original source/ID and
retained acceptance identity are validated before admission. Acceptance IDs stay
decimal strings in durable receipts, including values above JavaScript's safe
integer range. Raw body bytes are bound exactly, so changed whitespace under the
same acceptance conflicts too.

## Node

```ts
import {createEventPipeline, postgresEventPipelineStore} from '@gregale/sdk-node/preview';
import {contract as eventContract} from './event-generated.js';
import {contract as pipelineContract} from './pipeline-generated.js';

const events = createEventPipeline(eventContract, {
  name: 'import-events', scope, subscriptionId, source: configuredProducerSource,
  contract: pipelineContract, token: backendToken,
  store: postgresEventPipelineStore((sql, values) => pool.query(sql, values)),
  verify: verifyAuthenticatedInternalDelivery,
  authorize: async (event, principal, action, signal) => {
    const record = await loadImmutableImport(event.data.import_id, signal);
    return record && await mayAccess(principal, record.owner_id, action)
      ? record.owner_id : null;
  },
  map: async (event, ownerId, signal) => {
    const record = await loadImmutableImport(event.data.import_id, signal);
    if (!record || record.owner_id !== ownerId) throw new Error('import_changed');
    return record.input;
  },
});

const outcome = await events.handle({headers, body: originalBodyBytes}, principal,
  {timeoutMs: 10_000, signal});
if (outcome.state === 'started') saveRunId(outcome.receipt.checkpoint.run_id);
```

`verify` must authenticate the actual delivery boundary before returning its
`EventConsumerIdentity`. `eventConsumerRequestFromHeaders` can parse authenticated
native context; it is not authentication. Do not accept forwarded context headers
from arbitrary public callers. Scope and subscription IDs come from owner
configuration, including the selected environment's subscription. Cloned/paused
subscriptions retain their native policy. Never expose backend tokens to browsers.

`authorize` checks the current application principal for `start`, `inspect` and
`reconcile`, including duplicates. Return the authorized owner ID or `null`.
Native event identity does not grant business privileges. Mapping must validate
owner scope, be deterministic for the retained event and avoid business effects.
Do not put remote starts inside transactional event-consumer callbacks.

## Python

```py
from faas_sdk.preview import EventPipeline, EventPipelineDelivery, PostgresEventPipelineStore
from event_generated import contract as event_contract
from pipeline_generated import contract as pipeline_contract

events = EventPipeline(event_contract,
    name="import-events", scope=scope, subscription_id=subscription_id,
    source=configured_producer_source, contract=pipeline_contract,
    token=backend_token, store=PostgresEventPipelineStore(query),
    verify=verify_authenticated_internal_delivery,
    authorize=authorize_current_owner, map=map_immutable_import)

outcome = await events.handle(
    EventPipelineDelivery(headers=headers, body=original_body_bytes), principal,
    timeout=10)
```

Python `verify(delivery)` returns an authenticated `EventConsumerRequest`;
`authorize(event, principal, action)` returns the owner or `None`;
`map(event, owner)` returns the typed pipeline input. Callbacks use normal asyncio
cancellation. Optional `httpx_args` configures the isolated native clients.
The PostgreSQL adapter takes `query(sql, values)` with `$n` placeholders and
returns a list of row dictionaries. The example includes a psycopg adapter.

## Install, retain and recover

Install [event-pipelines.sql](../sdk/request-contracts/event-pipelines.sql)
explicitly as the application database owner. It is also exported as
`eventPipelineSchema` / `EVENT_PIPELINE_SCHEMA`. Use a shared primary database;
each store query must be one acknowledged autocommit statement. Do not use a read
replica, an ambient transaction, or an in-memory store for production delivery.

Only a newly acknowledged reservation may send the initial native start. A
duplicate returns `started` with its checkpoint, or `start_unknown` without one.
`replayed` distinguishes a retained receipt from this caller's first admission.
Do not acknowledge consumer completion based on `start_unknown`; retain the
event/request in your application and arrange reviewed recovery. These are start
outcomes, not pipeline completion: use the existing typed run APIs for progress,
results, cancellation and approval views.

```ts
const current = await events.inspect(delivery, principal, {timeoutMs: 10_000});
const recovered = await events.reconcile(delivery, verifiedNativeRunId, principal,
  {timeoutMs: 10_000});
```

Python uses the same `inspect` and `reconcile` methods with `timeout=10`.
Reconciliation checks the exact native envelope, including the event trigger
identity, and only attaches its checkpoint. It never starts work. A lost reserve
reply may leave a receipt with no run at all; a lost start reply may mean the
pipeline is already running. Every later delivery remains unable to dispatch
again, even after native idempotency-key retention expires. Review native and
business evidence before taking any new action. Neither retrying inspection nor
reattaching a run continues a failed native code workflow.

Keep receipts for the entire time the event can be delivered or replayed.
Deleting a receipt or renaming/re-scoping an adapter permits fresh admission.
Receipt rows contain identities and digests, not original payloads or tokens;
protect identifiers and retain original events separately when needed for recovery.
Keep the business record immutable: changed bytes, owner, input, contracts or
configuration under an existing identity cause a receipt conflict.

Each operation requires one bounded total deadline. Node uses `AbortSignal`;
Python uses task cancellation. Late callback results cannot cause a later SDK
start, but cancellation cannot undo a start already accepted remotely. Configure
database query timeouts and honor cancellation in user callbacks.

## Full local flow

See [the CSV example](../examples/sdk-csv-jobs/README.md#event-triggered-reviewed-imports).
It exercises `import.requested` -> durable start -> reviewed pipeline -> mixed
Node/Python Jobs -> private browser approval/progress -> verified report, including
concurrent consumers, a lost start response and recovery by the other SDK.
Native event delivery, control-plane and realtime are local protocol fixtures;
this does not establish deployed broker/scheduler acceptance.
