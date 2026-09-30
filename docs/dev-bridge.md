# Gregale Dev Bridge

Dev Bridge runs one HTTP service on your laptop while the rest of its application
runs in a Gregale development environment. It is an internal, operator-gated
capability under [ADR-377](adr/377-development-bridge.md). The implementation is
ready for a controlled development trial; native fleet acceptance and a public
product rollout have not been performed.

## Use the bridge

Start payments in your IDE on port 8080, then run:

```sh
gregale dev bridge payments --environment development --local-port 8080 \
  --entrypoint frontend --inspect
```

The CLI prints a loopback **Session URL** for the remote frontend and a loopback
HTTP URL for each discovered dependency. Open the Session URL in your browser.
The remote frontend can call local payments; configure the local process with
the printed dependency URLs to call remote inventory and other APIs. Normal
requests to the environment keep reaching its deployed services.

The selected app must be an active project app. The environment must already
exist, be unprotected, and have deployed revisions for its remote services.
`production` and `default` are rejected. Private apps are accessible only in the
verified session scope. Normal application authentication and gateway policy
still apply. Scoped responses bypass shared caches, edge answers and shadow
mirroring. This initial implementation forwards HTTP requests and streaming
bodies; it does not forward database protocols, arbitrary TCP, WebSockets or gRPC.

Service bindings are discovered by default. `--dependencies orders,inventory`
replaces that default with an explicit set of same-project apps. `--entrypoint`
adds the remote frontend to the allowed graph while retaining discovered
bindings. Include every remote service that participates in a scoped call chain.
There is no automatic expansion to unrelated project services.

Sessions last one hour. Ctrl-C revokes the session; expiry also closes its
connection. The CLI reconnects within the same lease, and a new attachment fences
the old connection. Requests fail explicitly while disconnected and are never
retried against the deployed service. Existing edge and remote-service request
deadlines still apply when debugging through those hops. An interrupted request
may have reached application code; application idempotency still matters.

## Propagate the development session

Remote services must opt into propagation on synchronous HTTP calls. Gregale
cannot infer a developer's session from a trace ID or a background job. The Go
SDK provides middleware and a transport:

```go
import (
    "net/http"
    faas "github.com/poyrazK/faas/sdk/go"
)

client := &http.Client{Transport: faas.DevBridgePropagationTransport{}}
handler := faas.DevBridgePropagationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    request, err := http.NewRequestWithContext(r.Context(), "GET", "http://payments.svc.gregale/charge", nil)
    if err != nil { http.Error(w, "request failed", 500); return }
    response, err := client.Do(request)
    if err != nil { http.Error(w, "payments unavailable", 503); return }
    defer response.Body.Close()
    // Consume the response as usual.
}))
_ = handler
```

Use the incoming request's context. The transport forwards request authority
only to single-label Gregale service names (`SERVICE.svc.gregale` and
`SERVICE.internal`) and removes it from third-party requests. Platform code has
equivalent helpers in `pkg/devbridge`.

The node-local service proxy first verifies the caller's actual instance identity
and its normal service binding. It then verifies session membership and that the
caller's deployment belongs to the selected environment. A production deployment
cannot use this session to reach the laptop. Each routing boundary rechecks
account status, environment eligibility, app ownership and the session lease.
Release/revision overrides conflict with session routing and return 409.

## Inspect requests and copy a webhook

`--inspect` prints a separate loopback inspection URL. It shows the last 100
requests reaching the local process: method, redacted path, status, duration and
response byte count. Pending streamed responses remain visible. Query strings,
headers and bodies are excluded; nothing is persisted by this inspector.

To copy one existing provider-verified inbound webhook receipt:

```sh
gregale dev bridge payments --environment development --local-port 8080 \
  --inspect --replay-webhook INVOCATION_ID
```

The receipt must be owned by the account and belong to payments. This is an
explicit copy of a selected delivery, including a receipt originally received in
production; the destination is always the development session. The original
payload, delivery state and retry attempts are untouched. The copied request
carries development replay/original receipt metadata and the original content
type. Expired provider signatures and original authorization headers are omitted.
The local application must accept the platform's development replay contract.

The CLI prints the replay receipt ID before dispatch. The API records a receipt
before sending, and repeating its idempotency key never dispatches again. A
`completed` receipt records an HTTP response; check `http_status` for application
success. `uncertain` means dispatch may have reached the local process. A crash
can leave `dispatching`; inspect local effects before deliberately using a new
key. Reconnect never automatically replays the selected webhook.

## Control API and transport

The OpenAPI reference and Go, Node and Python SDKs describe these account-scoped
control operations:

| Operation | Route |
| --- | --- |
| Create session | `POST /v1/dev/bridges` |
| Inspect session | `GET /v1/dev/bridges/{id}` |
| Revoke session | `DELETE /v1/dev/bridges/{id}` |
| Copy selected webhook | `POST /v1/dev/bridges/{id}/webhook-replays` |
| Inspect replay receipt | `GET /v1/dev/bridges/{id}/webhook-replays/{replay}` |

Creation returns separate attachment and request credentials exactly once.
Inspection returns scope, expiry and revocation state without credentials. The
webhook copy body includes `invocation_id`, `request_token` and
`idempotency_key`; the receipt ledger stores no payload or credential.

The CLI uses a scoped protocol proxied through apid:

| Protocol | Authority |
| --- | --- |
| `GET .../{id}/connect`, WebSocket subprotocol `gregale-dev-bridge-v1` | Attachment token; browser Origin denied |
| `GET .../{id}/status` | Attachment token |
| HTTP `.../{id}/traffic/{path...}` | Request token |
| HTTP `.../{id}/dependencies/{app}/{path...}` | Attachment token plus declared dependency |

These endpoints require `X-Gregale-Dev-Bridge-Account` and
`X-Gregale-Dev-Bridge-Token`. Environment routing also uses
`X-Gregale-Dev-Bridge-Session`. `X-Gregale-Dev-Session-Context` carries the request
credential across opted-in service calls; it cannot attach a laptop. These are
secrets and must not be logged. Raw attachment headers and the dashboard cookie
are stripped before application hops. The CLI keeps credentials out of browser
URLs, terminal output and query strings; its loopback proxies reject foreign
Host/Origin values.

## Operator installation and rollback

1. Apply the two append-only bridge migrations through the normal apid migration
   workflow. Build the release with `make build`; its daemon list includes
   `bridged`.
2. Set `faas_dev_bridge_enabled: true` for the control-plane and compute gateway
   Ansible roles. The default is false. The control-plane role installs the
   optional relay unit, its dedicated environment file, apid's feature drop-in
   and the database peer identity. The gateway role installs its feature drop-in.
3. Check the private gateway/API paths and deployed development revisions.
   Explicitly enable/start `faas-bridged.service` on the control plane. It is
   excluded from automatic core fleet activation.
4. Exercise the acceptance scenario below in a private development environment
   before making the capability available to developers.

`bridged` runs as `faas-bridged`, without VM privileges or sealed secrets. It
requires a control-plane or single-box role, a literal loopback listener
(default `127.0.0.1:9098`), and a database pool with
`default_transaction_read_only=on`. apid remains the writer of session intent and
replay receipts. The generated unit includes resource limits, restart throttling
and the existing systemd hardening policy.

| Setting | Purpose/default |
| --- | --- |
| `FAAS_DEV_BRIDGE_ENABLED=1` | Required on apid, bridged and internal gateways |
| `FAAS_BRIDGED_ROLE` | `control-plane` or `single-box` |
| `FAAS_DEV_BRIDGE_ADDR` | Relay loopback bind, `127.0.0.1:9098` |
| `FAAS_DEV_BRIDGE_RELAY_URL` | apid's loopback relay, `http://127.0.0.1:9098` |
| `FAAS_DEV_BRIDGE_GATEWAY_URL` | Literal loopback dependency gateway, `http://127.0.0.1:8080` |

Compute gateways reach apid using their existing private upstream configuration.
Public ingress stays with gatewayd-public; no public relay port is added. This
scope uses one control-plane relay owner. Multiple relay replicas need owner
discovery and are outside this initial implementation.

Rollback by disabling the flag on apid/internal gateways and stopping the relay.
The Ansible disabled configuration stops and disables the unit. Scoped requests
fail closed while ordinary routing continues. Revoke sessions before disabling
if receipt/session inspection is needed; otherwise their leases expire. Keep
additive tables during a rolling rollback.

The limits live in `pkg/api/limits.go`: 32 declared dependencies, 32 concurrent
requests per connection, 32 KiB request headers, 100 inspection records, and 100
webhook copies per session with a 30-second replay deadline. Account session
admission uses the plan's existing `DeveloperApps` limit. When the account creates
another session, sessions expired for over seven days and their replay receipts
are pruned transactionally; inactive accounts retain their last metadata until
further activity or account deletion. Replay inspection is available after
revocation while the metadata remains retained.

## Acceptance evidence

The local application scenario uses real HTTP servers, the gateway/service proxy,
WebSocket relay and local process transport. It verifies remote frontend → Alice's
local payments → remote inventory, an independent Bob session, ordinary remote
routing, and denial of a production caller. The source-instance identity resolver
is a fixture; this is not native microVM acceptance.

Additional focused tests cover streaming bodies across API and public HTTP proxy
hops, request cancellation, connection replacement fencing, idle revocation,
private environment admission, preserved
application authentication, account/environment isolation, MemStore/PostgreSQL
quota parity, concurrent replay deduplication, original webhook preservation and
credential redaction. Native split-box installation, edge WebSocket lifetime and
real VM identity checks remain rollout acceptance gates. The complete repository
suite has not been run as part of this feature check.
