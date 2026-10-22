# Durable entity developer workflow (Node)

This starter registers five namespaces: `counters` for customer coordination,
`reservations` for state plus an outgoing webhook intent, `sessions` for
scheduled automation, `rooms` for reconnectable ordered sessions, and
`recoverable-rooms` for status, expiry and generation-fenced reclamation. State is
schema-versioned. Handlers compute transitions
synchronously; external delivery belongs to the platform outbox.

`recoverable-rooms` uses schema version 2, generation-qualified frames, idle expiry
after 10 logical ticks and retention for 5 ticks after closure. The authenticated
service explicitly advances the persisted clock; expiry is not automatic. Fresh
status query IDs recover committed progress. Reclaim only a fully closed retained
cohort, then deliberately use its new generation. See the
[recovery and lifecycle guide](https://github.com/poyrazK/faas/blob/main/docs/durable-entity-session-lifecycle.md)
for the command format and application migrations. The original `rooms` contract
remains available for existing clients.

## Create and prepare

```sh
gregale init --template durable-entity-node --path my-entities
```

The internal SDK is not published for public installation. In a Gregale checkout,
build and pack the SDK, then copy the resulting tarball into your generated project:

```sh
cd /path/to/faas/sdk/node
npm ci --ignore-scripts
npm run build
npm pack --ignore-scripts
cd /path/to/my-entities
mkdir -p packages
cp /path/to/faas/sdk/node/gregale-sdk-node-0.1.0.tgz packages/gregale-sdk.tgz
npm install --ignore-scripts
npm run generate:client
npm run prepare:validator
npm run typecheck
npm test
```

Keep `packages/gregale-sdk.tgz` and `package-lock.json` with the project. Reinstall
and regenerate the validator after changing the SDK or validator source files.
`gregale.entities.json` declares compiled modules, exports, namespaces and schema
versions. `registry.mjs` loads those declarations and checks their metadata.
The example handlers use checked JSDoc; TypeScript handlers can use the same SDK,
provided their compiled JavaScript modules are included in the deployment.

## Develop without cloud credentials

```sh
npm run dev
curl -s http://127.0.0.1:8787/invoke -d '{"namespace":"counters","key":"customer:123","request_id":"increment-1","payload":{"delta":1}}'
curl -s 'http://127.0.0.1:8787/inspect?namespace=counters&key=customer:123'
```

Repeat the identical request to replay its original result. Reusing its ID with
a different payload fails. Calls to one entity serialize; different keys have
independent state. Add `"fault":"before-commit"` to simulate a failed commit or
`"fault":"after-commit"` to simulate a lost acknowledgement. Retry with the same
ID and payload and omit the fault.

To schedule a session, invoke namespace `sessions` with payload
`{"action":"start","at":"2000-01-01T00:00:01Z"}`. Then POST
`{"milliseconds":1000}` to `/advance`. The virtual clock starts at
2000-01-01 UTC; alarms only run when explicitly advanced or drained through the
SDK. To reserve, invoke namespace `reservations` with payload
`{"action":"reserve","quantity":1,"webhookId":"<registered webhook UUID>"}`.
The simulator records the intent; it does not deliver webhooks.

```sh
curl -s http://127.0.0.1:8787/checkpoint > local-checkpoint.json
# Restart npm run dev, then restore into the empty simulator:
curl -s http://127.0.0.1:8787/checkpoint --data-binary @local-checkpoint.json
```

Checkpoints contain private application state and replay history. Keep them out
of source control and deployments; the generated ignore files exclude the name
above. They are local simulator artifacts, not platform backups or restore inputs.

## Review and deploy

```sh
npm run prepare:validator
gregale doctor
gregale deploy --name my-entities --doctor-strict
```

The CLI rejects incomplete declared projects before source upload, including
missing compiled modules, SDK/lock mismatches, unsafe paths and stale validator
bundles. `--no-doctor` does not bypass these declaration checks. The checks read
artifacts without executing customer code; they do not prove that a declared
JavaScript export exists. Startup checks the actual registry.

Deployment still requires operator-enabled, qualified durable-entity previews
and the existing immutable deployment/release bindings. Preparing
`gregale.validator.json` does not enable a restore validator, bind a release or
turn on a feature gate. Use the owner API/SDK durable entity handle to invoke a
qualified deployment with a stable request ID. Guest handler routes are platform
protocol endpoints and do not substitute for the authenticated entity API.
`gregale init --deploy` is intentionally blocked until preparation is complete.

The production server serves `/healthz` and the bounded entity/restore-validation
protocol routes. Local invocation, checkpoint and clock routes live in `dev.mjs`,
which the generated `.gregaleignore` excludes alongside tests and tooling.

## Limits of local simulation

The simulator checks protocol transitions, replay, schema migrations, alarm
consumption, intent capture and approximate payload budgets. It uses in-memory
storage, local receipt history and simulated intent IDs. It does not emulate
bucket CAS, ownership fencing, leases, worker crashes, multiprocess execution,
provider quotas, relay retries, admission or real storage durability. Native
KVM/GCS qualification remains a separate release requirement. Pure callbacks are
a programming contract; the local simulator is not an isolation sandbox.

After deployment, use `gregale entities inspect --app my-entities --namespace
counters --key customer:123` or `gregale entities watch` with the same selectors
for committed version, alarm/outbox recovery and delivery diagnostics. See the
[visibility guide](../../../../docs/durable-entity-visibility.md).


## Generated typed clients

```js
import {counter, reservation, session} from './client.mjs';
// Configure FaaSClient once with your existing authenticated API connection.
const customer = counter({slug: 'my-entities', key: 'customer:123', environment: 'production'});
const result = await customer.invoke('increment-1', {delta: 1});
console.log(result.value.count, result.version, result.replayed);
const observation = await customer.observe();
```

`npm run generate:client` reads `gregale.entities.json` and writes checked JavaScript
factories. Each handler declares a caller-only `client.module` and `client.export`.
The shared contracts infer payload/result types from their decoder functions; no
separate handwritten payload interfaces or result decoders are required at calls.
The supplied tsconfig checks generated clients and compile-only negative examples.
Keep `allowJs` enabled when importing these checked JavaScript contracts into a
TypeScript application. This generator currently targets Node, not browsers.

Contract modules contain only decoders and namespace metadata, not guest handlers.
Changing a declaration requires regeneration; `npm run generate:client:check`
checks drift without rewriting files. Generation never imports customer code.
Handlers use the same contracts for invocation payload validation; alarm decoding
remains a guest concern. CLI source checks validate declared contract files and
require the SDK client export when those declarations are present.

Client validation happens before transport, preserves strict JSON input and rejects
invalid payloads. A result decode error can follow a committed invocation; it
retains `version` and `replayed` through `DurableEntityResultDecodeError`. Retain the
request ID and original payload and retry that identical call after an uncertain
response. Generated clients never create a new request ID or automatically repeat
an invocation. Existing SDK transport retry settings still apply. `retry()` remains
an explicit fenced alarm/outbox recovery operation, distinct from invocation replay.

The `rooms` namespace demonstrates durable session identities and ordered counter
messages. See [reconnectable sessions](../../../../docs/durable-entity-sessions.md)
for frame preparation, acknowledgement checks, authenticated socket routing and
preview limits. This is separate from the scheduled automation `sessions` example.
