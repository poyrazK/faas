# Gregale Dev Bridge

Implementation is in progress under ADR-377. This is an operator-gated
development feature; it is not yet a released product capability.

Run one HTTP service on your laptop while its dependencies remain in an
unprotected named development environment:

```sh
gregale dev bridge payments --environment development --local-port 8080
```

Start the local process with your IDE or ordinary development command. The CLI
opens an outbound authenticated connection and prints a loopback session URL.
Requests through that URL carry scoped routing credentials to the environment's
normal gateway; matching requests reach the local process. Ordinary requests
continue to the deployed app. The gateway keeps application authentication,
request policy and limits before selecting the laptop. There is no automatic
fallback to the deployed app if the bridge disconnects.

The selected app must belong to a project. The named environment must already
exist and have a deployed revision for normal gateway routing. Production,
default and protected environments are rejected. Sessions expire after one
hour and Ctrl-C revokes the session. Revocation is checked before dispatch and
also closes idle laptop connections.

Declared service bindings are discovered by default. `--dependencies orders,inventory`
selects an explicit set of apps in the same project. The CLI prints a loopback
HTTP endpoint for each dependency; use these endpoints in the local app's
configuration. Only the allowed app IDs and selected environment can be reached.
Database protocols require a separate forwarding contract.

## Operator setup

Build and run `cmd/bridged` on the control plane as an unprivileged service.
Its listener is loopback-only and its database access is read-only session and
resource verification. apid owns session creation and revocation.

Enable `FAAS_DEV_BRIDGE_ENABLED=1` on apid, bridged, and gatewayd-internal.
apid uses `FAAS_DEV_BRIDGE_RELAY_URL` (default `http://127.0.0.1:9098`).
bridged uses `FAAS_DEV_BRIDGE_ADDR` (default `127.0.0.1:9098`) and
`FAAS_DEV_BRIDGE_GATEWAY_URL` (default `http://127.0.0.1:8080`) for dependency
requests. Compute gateways use their existing apid upstream configuration to
reach the relay through apid. Public ingress remains with gatewayd-public.

The database migration adds account-scoped sessions with separate credential
digests for laptop attachment and request routing. Inspection never returns
credentials. Live connections are disposable; session intent survives a process
restart. The current relay deployment requires one connection owner; shared
ownership discovery for multiple relay replicas is still outstanding.

## Verification and remaining work

Tests cover two independent streaming laptops, account/environment isolation,
normal versus intercepted routing, application authentication preservation,
private environment admission, PostgreSQL session quotas and revocation, and
the API-to-laptop flow with idle connection revocation. No VM lifecycle changes
are required for the bridge transport.

Session propagation through remote services, selected webhook replay, request
inspection, reconnect UX, deployment packaging, API reference updates and full
application scenario acceptance remain required before feature completion.
