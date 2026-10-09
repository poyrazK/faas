# ADR-848: Typed durable entity SDK handles and guest transitions

Status: Implemented locally; verification pending.

## Decision

Add Go and Node convenience SDKs over the existing invocation, inspection,
recovery and v1/v2 guest contracts. They introduce no server API, object-storage
schema, allocation policy or feature enablement change. Python retains its
existing generated invocation/inspection/recovery clients; typed guest helpers
are scoped to the languages that already implement guest protocol validation.

Client handles bind the app slug, namespace/key, current environment selector
and optional customer selector. The handle snapshots these selectors so caller
mutations or a supplied recovery body cannot redirect it. It binds a logical
entity, not a VM, permanent disk, owner or provider path. Account/environment
ownership remains server-verified. Node uses the SDK's existing global
`FaaSClient` transport configuration and does not independently bind credentials.

Invocations require an explicit request ID; handles never generate one. Payloads
are serialized before asynchronous transport/retries. After uncertain outcomes,
callers retain the same ID and exact payload. Go preserves uint64 versions;
Node rejects imprecise numeric versions. A typed result decode error can occur
after a successful commit. Go returns acknowledged version/replay metadata with
the error; Node throws `DurableEntityResultDecodeError` with those fields and the
local cause. Callers must not infer non-commit from SDK validation failure.

Inspection and retry preserve bound selectors. Retry requires caller-observed
version/recovery revision and exact target; the handle never fetches a newer
fence or automatically chooses different work on conflict. Existing owner-only
mutation admission and preview gates remain authoritative.

## Guest computation

Go `DecodeDurableEntityCall[State, Payload]` and Node `decodeDurableEntityCall`
first validate the existing guest envelope. Version zero uses a JSON-isolated
initial state supplied by application code; committed state is decoded as stored
and is never replaced by initialization on decode failure. Go uses normal JSON
type decoding; applications still validate required business fields/schema
versions. Node requires state/payload decoders, rather than treating generic
types as runtime validation. Payload decoding receives the event in Node to
support distinct invocation/alarm business input shapes.

The call exposes verified identity, normalized event, request ID, version and
local typed state/input values. It grants no bucket, lease or commit authority.
Applications keep schema migration and business validation explicit.

`call.Transition` / `call.transition` creates a pure builder. Its default is to
preserve the existing alarm. Schedule and clear operations are explicit; this
convenience default intentionally differs from the low-level wire encoder's
omitted-alarm-clears semantics. An alarm handler must explicitly clear or
replace its deadline if it should stop being due after committing.

Builders append registered webhook intents with captured JSON payloads, never
performing sends or other I/O. A rejected webhook poisons its builder: catching
or ignoring that error cannot publish an otherwise valid partial transition.
Node also poisons a builder if a Date cannot be serialized for scheduling.
Encoding delegates negotiated versions and byte/batch bounds to existing guest
helpers. v1 refuses outgoing work; v2 still requires platform opt-in. Encoding
returns bytes/string only, not a persistence acknowledgement. Application code
must remain pure because computations may repeat after rejected/uncertain commits.

## Example and qualification

`examples/durable-entity-sdk/counter.ts` demonstrates local counter transitions,
validated typed input/state, alarm preservation and a reminder intent with an
explicitly consumed alarm. It must be mounted behind the existing private guest
handler route; envelope decoding does not authenticate a public endpoint.

Written SDK cases cover scope snapshots, explicit replay identity, invalid JSON
inputs, result errors retaining commit metadata, isolated initialization,
committed-state decode failures, alarm preserve/schedule/clear behavior, captured
webhook payloads, rejected-intent poisoning and v1 outgoing-work rejection.

No tests, builds, lint, native runtime or live bucket checks were run, per the
user's instruction. Formatting and diff review are not qualification evidence.
The testing agent should run nested Go SDK tests and Node suites including
`durable-entity-typed.test.ts`, compile the example against SDK declarations, and
qualify the full commit/replay/alarm/outbox/recovery flow with native/provider
acceptance. Check application schema failures after acknowledgement and custom
JSON payload serializers during retry. Work remains local with no PR or deploy;
all preview/worker defaults remain unchanged.
