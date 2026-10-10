# ADR-939: Application-state schema migrations in the durable entity SDK

Status: Implemented locally; verification pending.

## Decision

Add opt-in Go/Node guest helpers for application-owned state envelopes
`{schema_version, data}`. Application schema version is a positive uint32,
separate from Gregale's uint64 business commit version, guest protocol version,
and private snapshot/manifest format. There is no platform migration worker,
SQL migration, object-store rewrite or new server API in this slice.

Applications provide a current schema version, pure initial-state factory,
current-state validator/decoder and pure `n -> n+1` migration functions. Wrapped
committed state must contain exactly the schema marker and data. Future schema
versions, malformed envelopes and missing upgrade steps fail closed. The complete
chain is checked before any migration callback runs. Callback functions and target
version are captured before execution; mutating configuration cannot redirect
an in-progress upgrade.

Unwrapped committed state is rejected unless `LegacyVersion`/`legacyVersion`
explicitly identifies its starting schema. A legacy starting version of zero is
allowed if all required steps are supplied. Even adopting legacy data at the
current schema is marked as a migration because it needs the envelope persisted.
Version-zero entities initialize directly at the current schema; committed decode
failures never reset state. Current application data is validated after upgrade
and again when encoding the next transition. Node requires synchronous JSON
transformations and rejects promise/thenable outputs. Both implementations reuse
existing negotiated transition byte bounds for intermediate/final data, including
envelope overhead, instead of allowing unbounded intermediate state.

## Publication and pure computation

Migration preparation performs no I/O and does not persist state. The schema call
exposes the upgraded local state, stored schema version and migration indicator,
retaining Gregale's original identity, event, request ID and commit version.
Its transition builder always wraps the updated data with the configured current
schema. Alarm preservation/schedule/clear and registered webhook intent helpers
retain ADR-938 behavior. A successful migration and business transition publish
through the existing ownership-fenced state/result/receipt/alarm/outbox commit.
Any handler or commit failure leaves committed state unchanged; computation can
repeat after rejection or uncertain outcomes. Already-committed receipt replays
still return original results without running migration or guest code.

Migration functions, initialization and validation must be deterministic and pure.
They cannot send notifications or make external writes. Encoding a transition is
not commit acknowledgement. Applications remain responsible for required fields,
semantic validation and schema-specific compatibility; Go's ordinary struct
unmarshal is not a JSON Schema validator and may accept absent fields unless
applications explicitly validate them.

## Release and rollback contract

This is a cooperative application SDK contract, not platform enforcement of
arbitrary opaque state data. Schema-aware older handlers reject newer envelopes;
legacy handlers that do not understand envelopes can still overwrite them through
the existing low-level API. The SDK cannot revoke an old application deployment.

Before writing a newer schema, first deploy schema-aware readers/writers to every
invocation and alarm path at the existing schema, with explicit legacy adoption
if needed. Ensure older unaware deployments and in-flight guest work have stopped
before enabling upgrades. Then deploy the next schema and its upgrade chain.
Mixed schema-aware releases fail safely when an older release encounters newer
state, but this can reduce availability. Once new-schema state commits, rollbacks
must retain new-schema support or use an explicitly designed application data
rollback procedure. No automatic downgrade or destructive fallback is provided.

## Example and verification handoff

`examples/durable-entity-sdk/migrating-counter.ts` upgrades wrapped version-one
`{total}` state to version-two `{count}` and commits the envelope with the normal
counter transition. It does not implicitly admit the older unwrapped example;
applications must choose and validate their own explicit legacy mapping.

Written SDK cases cover initialization, multi-release current-state reads, local
upgrade preparation, wrapped transition/alarm/outbox output, future-schema
rejection, missing steps before callbacks, malformed/zero envelopes, legacy
adoption, callback errors, oversized state, invalid current/next data, and async
transformation rejection.

No tests, builds, lint, native qualification or live bucket checks were run, per
user instruction. Formatting and diff review are not qualification evidence.
The testing agent should run nested Go SDK tests and Node suites including
`durable-entity-schema.test.ts`, compile both typed examples, and exercise
migration plus business transition through real engine admission/commit/replay.
Include CAS rejection, lost acknowledgement, alarm execution, current schema
reads after restart, future-aware rollback rejection, the staged legacy rollout
contract, negotiated size limits and no extra outgoing intents on receipt replay.
Work remains local with no PR or deployment; feature gates remain unchanged.
