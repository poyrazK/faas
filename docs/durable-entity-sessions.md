# Reconnectable entity sessions

This first preview provides an owner-authenticated WebSocket transport and a pure
application-session contract. It does not provide end-user socket credentials,
server broadcasts, presence, push subscriptions or hibernation. Native KVM/GCS
qualification remains required before production preview enablement.

For status recovery, logical-time expiry, safe cohort reclamation and application
migrations, use the opt-in [recoverable session contract](durable-entity-session-lifecycle.md).
The version-1 helper below retains its original behavior.

## Transport

The operator may enable `FAAS_DURABLE_ENTITY_SOCKETS_ENABLED=1` only alongside the
existing durable entity preview and explicit app allowlist. The default is off.
The owned app must have WebSockets enabled and an eligible plan. Connect with an
account credential carrying the same MFA and deploy-write permissions as entity
invocation:

`wss://<api-host>/v1/apps/<slug>/entities/connect?namespace=rooms&key=document:123`

The endpoint accepts the inspection selectors `namespace`, `key`, optional
`environment` and `platform_tenant_id`, each exactly once. Browser Origin headers
are refused. Do not embed an owner API credential in browser code or URL queries.
Service clients can use the Node `ws` package with an Authorization header. End-user
authorization/broadcast support requires a separate future design.

A text frame contains `{protocol_version:1, request_id, payload}`. The response
contains `{protocol_version:1, request_id, status, result?, problem?}`. A successful
`result` is the existing entity invoke response with `value`, `version`, `replayed`.
The transport routes through normal authenticated entity invocation for each frame,
rechecking credentials, scopes, MFA, tenant status and deployment admission. It also
rechecks app WebSocket eligibility. Authorization rejection closes the connection.

There is one in-flight invocation per connection and no application queue. Request
frames are capped at 64 KiB; responses are bounded by the existing invocation byte
budget. Writes time out after five seconds and idle reads after thirty seconds.
Connections last at most ten minutes plus any already-running bounded invocation.
Per API process there are at most 64 connections, 16 per account and four per exact
entity scope. These are process limits, not fleet-wide admission quotas. Malformed,
binary or oversized frames close the socket. Successful acknowledgement follows
storage commit; a disconnect or an error can still leave an uncertain committed
outcome. Preserve the original frame and replay it after reconnect.

## Durable application sessions

The starter's `rooms` namespace uses `defineDurableSessionEntity` to persist an
application counter plus up to 32 session identities. A canonical UUIDv4 session
ID is stable across connections and is not an authentication credential. The
helper's state schema is version 1; application schema migrations, session expiry
and tombstone reclamation are not provided in this first contract.

```js
import {durableSessionFrame} from '@gregale/sdk-node/preview/durable-entities/session';
const open = durableSessionFrame({kind:'open',session_id:'11111111-1111-4111-8111-111111111111'});
const message = durableSessionFrame({kind:'message',session_id:open.payload.session_id,sequence:1,payload:2});
// Persist JSON.stringify(message) privately before sending it, then send those
// exact bytes. On reconnect, resend that saved frame instead of minting an ID.
```

To connect using `ws` (install it in the service client project):

```js
import WebSocket from 'ws';
const socket = new WebSocket(url, {headers:{Authorization:`Bearer ${ownerToken}`}});
socket.on('open', () => socket.send(savedFrameBytes));
socket.on('message', bytes => console.log(JSON.parse(bytes.toString())));
```

Open once. For subsequent messages use consecutive positive sequence numbers,
advancing only after saving a successful committed acknowledgement. Explicit close
uses the next sequence number. Request identities are deterministically derived
from session ID, command kind and sequence. Sending a new ID for an old sequence,
changing a replay's payload or sending a sequence gap fails. Old committed frames
may still replay after close; no new frame may mutate a closed session.

Keep at most one outstanding frame per logical session, even across sockets. The
entity serializes state transitions across connections. Ordering between different
sessions is commit order, not wall-clock arrival order. Do not reconnect by replaying
open and treating its original sequence-zero acknowledgement as current progress;
retain the last acknowledged sequence and the outstanding frame instead. This
first helper does not expose a server-side resume cursor query.

Closed sessions remain as tombstones in the 32-identity lifetime budget. Once full,
new sessions are refused; no automatic deletion/reuse silently loses ordering
history. Explicit future reclamation needs a replay-retention contract. The session
result is `{session_id, sequence, closed, value}`; open and close have value=null.
Normal entity inspection/observation exposes entity-level recovery metadata.

For local development, send the frame's request_id and payload to the starter's
`/invoke` route, selecting namespace `rooms`. Local checkpoint restart validates
session semantics without WebSocket or native storage emulation. Keep saved frame
files private; the generated ignores exclude `.session-frame.json`. Save other
filenames only after adding them to both source-control and deployment ignores.

## Qualification

[CI run 38124353085](https://github.com/poyrazK/faas/actions/runs/38124353085)
passed for source `2422e3b765195342b234510ea9d69c8bbea3f6b2`.
Coverage includes authenticated socket routing, reconnect replay, per-frame
app eligibility, origin rejection, connection slots and malformed frames. SDK and
packed-starter coverage exercises lost acknowledgements, state restoration, sequence
gaps, closed sessions, bounded identity retention and isolated restore validation.
Local tests/builds are deferred; native takeover, relay, credential revocation and
service restarts still require acceptance evidence. Existing production gates remain
off. Socket loss never means an invocation did not commit.

Use `decodeDurableSessionAcknowledgement(reply, savedFrame, decodeValue)` before
advancing the saved sequence. It checks request/session/sequence identity, closure
state and safe committed versions. A failed acknowledgement cannot authorize a
cursor advance. Decoder failures may follow successful commits; retain the saved
frame. Open/close results have null value, so decoders must accept it when those
commands are pending.

For end users, use the separate opt-in [scoped browser session contract](durable-entity-browser-sessions.md).
A trusted application backend authorizes the user and issues a short-lived grant;
owner API credentials remain server-side. Lifecycle expiry/reclamation stays service-only.
