# Typed durable entity SDK example

[counter.ts](counter.ts) is a pure Node guest handler using the new typed call
helper. It initializes a new counter, validates persisted state and invocation
payloads, preserves alarms during ordinary transitions, and consumes a due alarm
by returning a registered webhook intent. It does not send a notification.

This is source code for integration into an app's existing trusted
`POST /__gregale/entities` guest handler, not a deployed or qualified application.
Bound the HTTP body, use the established private runtime path, and use a
registered webhook UUID from app configuration. Envelope parsing alone does not
authenticate a public endpoint. The reminder branch requires guest protocol v2;
v1 calls reject outgoing work. Existing preview and worker gates still apply.

Configure `FaaSClient` with account credentials before constructing a client
handle:

```ts
import { durableEntityHandle } from '@gregale/sdk-node';
const counter = durableEntityHandle<{ delta: number }, { count: number }>(
  { slug: 'counter', namespace: 'counters', key: 'customer:456', environment: 'staging' },
  value => {
    if (value === null || typeof value !== 'object' || !('count' in value)
      || !Number.isSafeInteger(value.count)) throw new TypeError('invalid counter result');
    return { count: value.count as number };
  },
);
const result = await counter.invoke('stable-operation-id', { delta: 1 });
const inspection = await counter.inspect();
```

Keep the same operation ID and exact payload after uncertain responses. A result
validation error may follow a successful commit; the SDK retains version/replay
metadata in `DurableEntityResultDecodeError`. Do not submit a new request ID to
work around that error.

For exhausted outgoing work, use `counter.retry()` with the fresh inspection's
version, recovery revision and exact head ID. The handle retains its environment
and optional customer selectors, and does not silently refresh a stale fence.
Retry re-arms metadata only; it does not confirm receiver completion.

The Go SDK offers the corresponding `NewDurableEntityHandle`,
`DecodeDurableEntityCall` and pure transition builder. Python retains its
generated low-level invoke/inspection/recovery clients in this slice. Tests,
builds and native/provider acceptance have not been run here.

## Application schema upgrades

[migrating-counter.ts](migrating-counter.ts) uses the optional versioned state
helper. Wrapped schema 1 `{total}` upgrades to schema 2 `{count}`; new entities
start at schema 2. Migrated state commits only with the normal handler transition.
The example rejects future schemas and unwrapped committed state. Choose an
explicit `legacyVersion` only if the existing application's data really matches
that schema and its upgrade function validates required fields.

Deploy schema-aware readers/writers at the existing schema before enabling an
upgrade. An old handler that ignores this SDK contract is not fenced by schema
metadata and must stop before newer envelopes are written. After an upgrade,
rollbacks must retain support for the committed schema. See
[ADR-939](../../docs/adr/939-durable-entity-application-schema-migrations.md).

### Pure restore validation

`migratingCounterRestoreValidation` in `migrating-counter.ts` is a pure verdict
handler for the distinct `/__gregale/entities/validate-restore` private route.
Mount it separately from the normal business handler before an operator enables
application validation. It checks namespace, exact current schema and counter
invariants without I/O, migration or a transition. Keep the route private using
the same platform routing/authentication boundary; decoding an envelope is not
public-route authentication. See ADR-943 for the owner validation/restore flow.
