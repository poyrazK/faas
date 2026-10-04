# Total ordinary-HTTP deadline

ADR-531 implementation; complete-path acceptance is pending.

The existing `kind=budget` action accepts:

```json
{"budget_ms": 1000, "total_deadline_ms": 3000}
```

`budget_ms` remains the execution allowance after upload, wake, routing and
capacity admission. `total_deadline_ms` includes elapsed public ingress time,
upload, auth, cache/route lookup, queueing, wake, retry attempts and the ordinary
response exchange. The plan ceiling caps both. Zero or omission leaves the
total deadline unset. An execution override cannot increase the total deadline.
CLI create/update accepts `--total-deadline-ms`; retain `--budget-ms` too.

The private compute listener consumes a timestamp replaced by the public proxy.
Customer timestamp claims are overwritten and the transport header is removed
before policy/guest forwarding. A total rule is matched on the public route
after owner resolution and pinned through wake and execution. Elapsed policy
lookup time is charged once the rule is resolved; lookup still has the existing
platform envelope and store timeouts before the customer policy is known.

Before response commitment, expiry returns 504 with
`code: request_budget_exceeded`. Its best-effort socket write has a separate
100 ms allowance after expiry; a client that cannot receive it may see a
transport error. Expiry during upload closes the original body,
removes any spool file and prevents wake admission. Shared wake work may remain
bounded for other waiters. Ordinary responses abort on expiry after commitment;
the client sees a transport error for a truncated body rather than a clean
short success response. Bidi gRPC framing alone does not enable streaming.
Streaming and upgrades switch to their existing idle/session bounds after
successful response headers (101 for Upgrade). The gRPC hop receives the
independent session ceiling for a detachable response, while local context
cancellation bounds its handshake. Client cancellation remains effective.

Public and managed-service callers cannot author the internal streaming
control: the gateway clears caller flags and selects from the app protocol
and its streaming policy.

The protected compute response carries its absolute deadline and successful
session decision to the public proxy. The proxy takes the earlier of its own
deadline and the compute deadline and removes both private response controls.
Guest headers, edge header actions, content types and trailers cannot opt an
ordinary response into detachment. Invalid or ambiguous deadline metadata
refuses the response before commitment.

Socket/HTTP2-stream write deadlines and context cancellation release a copier
blocked on a client that has stopped reading, as well as one blocked reading
the guest. Cleanup joins the copier before releasing handler ownership.
Non-101 Upgrade refusals use the same ordinary response controls.

Roll out the compute gateway before the public gateway: the new public proxy
requires the compute-authored private session decision to detach a streaming
or gRPC handshake. An older compute gateway cannot supply that decision.

## Managed HTTP chains

An ordinary request with a total policy receives an opaque
`X-Gregale-Request-Deadline` carrier at the guest boundary. The public ingress
strips incoming claims. The node-local service proxy verifies the carrier
against the source app and its authorized account before wake, then signs the
same or an earlier absolute deadline for the receiving service. A binding
timeout can shorten that deadline; wake and retries cannot reset it. An
explicit binding timeout also establishes a chain for an otherwise unlinked
managed call. Long-lived sessions omit the ordinary chain carrier.

Applications must propagate request context. The platform cannot infer which
of a VM's concurrent requests caused an arbitrary outbound socket or task.
Omitting the carrier starts an unlinked call, so such a call does not inherit
the parent guarantee.

For Node:

```ts
import { createGregaleFetch, withGregaleRequestContext } from '@gregale/sdk-node';
const managedFetch = createGregaleFetch();
function handle(request: Request) {
  return withGregaleRequestContext(request.headers, () =>
    managedFetch('http://catalog.svc.gregale/products'));
}
```

For ASGI Python, `GregaleReleaseMiddleware` captures both release and deadline
context. Use `AsyncGregaleReleaseTransport` with HTTPX inside the handler. A
synchronous handler can use `with_gregale_request_context(dict(request.headers))`
with `GregaleReleaseTransport`. Both SDKs support `.svc.gregale` and declared
`.internal` aliases, isolate concurrent context and remove deadline carriers
from external calls. The current request's deadline wins over an explicit
downstream override. Node returns redirects from signed calls for the app to
follow through the helper; HTTPX reapplies its transport guard at each redirect.

Participating gateways require the same operator-provisioned 32-byte
`FAAS_SESSION_KEY`; a purpose-derived MAC key keeps this token protocol separate
from session cookies. Development's ephemeral session fallback cannot protect
a managed chain. A configured total policy or binding timeout with unavailable
material returns 503 `traffic_deadline_unavailable`. Invalid, ambiguous or
wrong-app/account carriers return 400 `traffic_deadline_invalid`; an authentic
expired carrier returns 504 `request_budget_exceeded`. No unsigned fallback is
used. The token proves a bounded deadline, not service authorization.

Clocks must be synchronized. An apparent future issue time is refused, and
coordinated master-key rotation can refuse old in-flight tokens. Cross-node
clock/transport/key-rotation acceptance, overload/complete-path integration
and rollout evidence remain required. Local real HTTP/1, HTTP/2 and gRPC
slow-reader checks passed, including the public production wrapper chain,
rejected Upgrade, cancellation without a deadline, source close and request/RPC
cleanup while the stalled client connection remained open. These fixtures do
not establish native VM/network or deployed acceptance.
Detached work, arbitrary guest sockets and background computation after a
disconnect are outside this request deadline.

## Configured gateway and node integration

A local Postgres process test connects configured compute gateways to the
production vmmd gRPC admission server and reusable bridges. The stored public
total allowance is 1.2 seconds; execution and declared dependency allowances are
five seconds. A guest fixture spends 300 ms before explicitly propagating the
carrier to another gateway. Its outgoing context is independent from the public
request, so caller cancellation cannot by itself prove downstream enforcement.
Authenticated observations verify the same chain/account, the correct receiving
apps and no later child deadline. The managed call returns a timeout, both node
permits release, and fresh public and managed work succeeds. A forged public
carrier is replaced before the guest receives it.

The fixture calls the same operator-key loader used by outer gateway startup.
Warm placement, source/listener translation, guest serving, namespace and VM
startup remain fixtures. This is explicit application carrier propagation;
the warm registry and deployment weights are loaded and checked before readiness.
SDKs, the separate public edge daemon, cross-host clock/key rotation, native
network/KVM, deployed load and staging retain separate acceptance requirements.
