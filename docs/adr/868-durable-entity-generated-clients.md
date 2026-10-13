# ADR-868: Durable entity generated clients

Status: Implemented; focused developer-workflow CI passed. Native acceptance pending.

## Decision

Add a Node client-contract entrypoint and declaratively generated namespace-bound
factories to the durable entity starter. Payload and result types are inferred
from shared decoder functions. Handler declarations identify a caller-only module
and export; generation reads those declarations without importing customer code.

Handlers and clients share invocation decoders, while alarms remain guest-only.
Generated checked JavaScript is included in the starter typecheck together with
negative type assertions. A check mode detects drift. Source admission checks
require regular contract modules and an SDK providing the new client entrypoint.

Calls require explicit request IDs, capture strict JSON before transport and
validate payloads without modifying the wire input. Result decode failures retain
commit metadata using the existing error type. Generated clients expose observe,
inspect and explicit recovery; they do not automatically replay calls or mint IDs.
Existing configured transport retries remain unchanged.

## Validation

Author packed-starter HTTP coverage for scope binding, validation before transport,
result decoding, stable replay identity and observation; compile-only assertions
verify inferred payload/result types. Go tests reject unsafe or unprepared client
artifacts. Local tests/builds remain deferred per user instruction. Generation and
formatting are permitted source maintenance; production gates remain unchanged.

Focused CI evidence and native acceptance boundaries are recorded in the
[release qualification report](../durable-entity-release-qualification.md).
