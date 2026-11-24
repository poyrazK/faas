# ADR-384 · Fetch-compatible internal service port

- **Status:** accepted
- **Date:** 2026-10-01
- **Issue:** #3941; release audit #3937
- **Amends:** ADR-169, ADR-170, ADR-269, ADR-274 (HTTP port only)

## Context

The canonical `GREGALE_SERVICE_<NAME>_URL` uses TCP port 10080. The WHATWG
[Fetch bad-port table](https://fetch.spec.whatwg.org/#port-blocking) blocks that port before DNS or network access. The
production Node Compose fixture fails with `bad port` using standard `fetch`,
while the otherwise identical three-service chain works using `node:http`.
Customers should not have to replace a standard HTTP client to call a binding.

## Decision

New canonical HTTP binding URLs use `<name>.svc.gregale:10081`. The existing
`service_proxy_listen` configuration accepts either reserved port and starts
both 10081 and legacy 10080 on that same private tenant-bridge address. Both
listeners share the exact guest service proxy handler, H1/H2C support, caller
source identity, same-account and binding checks, target policy, request limits,
and telemetry. DNS answers remain node-local; public and arbitrary host ports
remain closed. The per-netns firewall admits both reserved TCP ports only to
its configured host-bridge address. HTTPS remains opt-in and unchanged.

Existing persisted deployments and running process environments keep their
10080 URLs until redeployment. That listener remains supported; no bulk
customer intent rewrite is needed. Binding construction and the CLI network
view use the same canonical port constant.

## Rollout and rollback

Before activating a control plane that generates 10081 URLs, stage the signed
candidate on every compute host and verify both listeners and firewall paths.
Use the normal compute-stage gates, then the full `cd-platform` rollout and
fleet convergence gate. A control-plane-first mixed fleet is not qualified.
No serving-node destructive acceptance tests are allowed. After rollout,
redeploy the Node Fetch fixture, test same-node and cross-node calls, a parked
target, authorization rejection, and legacy 10080 calls. Host packet checks
must show that other private destinations and host ports remain denied.

The bridge listener is additive. Rolling back the control plane only restores
10080 generation; new compute hosts continue to serve both ports. Do not roll
compute back to a build without 10081 while a deployment still contains a new
10081 URL. First restore affected callers' binding environment through a
normal redeploy on the old control plane, then roll back compute if necessary.

## Alternatives

Disabling Fetch's port protection or documenting `node:http` as a requirement
pushes platform compatibility onto customer code. Promoting HTTPS by default
requires fully qualified service CA delivery and changes the transport contract;
it is a separate decision. Removing 10080 breaks persisted deployments.

## Evidence required

- Canonical binding construction and actual Node Fetch request succeed.
- Both listeners retain the same H1/H2C and authorization behavior.
- Firewall rules precede private-address denies and remain address/port scoped.
- Full compute-stage and fleet release gates check 10081 and 10080 listeners.
- Post-rollout real guests verify cold/warm, same/cross-node, legacy callers,
  and denied unbound/cross-account/private-host paths.

## Amendment 1 (2026-11-24): no TCP keepalive on guest connections

**Context.** The listeners accepted guest connections with Go's default TCP
keepalive: a probe every 15 s once a connection is idle. Each probe is a
packet for the guest, and an app that holds idle service connections is
resumed by it from quiet pause (ADR-20261107122928). A 10-minute capture
inside a paused app's namespace on bhs-3 (2026-11-24; the retail demo's
frontend, four idle gRPC connections to `<name>.svc.gregale:10081`) matched
11 of its 16 pause ends to these probes, and to the HTTP/2 GOAWAY the
server sent 60 s after the connections went idle, which the client answered
by reconnecting straight away. That woke the app about four times a minute
for as long as it stayed resident.

**Decision.** The service proxy listeners (10081, 10080 and the opt-in
HTTPS alias) turn TCP keepalive off on every accepted guest connection,
inherited listeners included, and close a guest connection after
`api.GatewaydInternalGuestServiceIdleTimeoutSecondsDefault` (300 s) idle
instead of 60 s. The HTTP server's idle timeout already closes idle
connections, and its read and write timeouts bound one in use, so the probes
added nothing. Handler, identity, policy, protocols and the other timeouts
are unchanged.

**Consequences.** A paused caller with idle service connections is woken
once per idle timeout (the close, then its reconnect) instead of about four
times a minute. A connection whose guest vanished without closing it stays
open until the idle timeout instead of the probes' roughly two and a half
minutes; that holds a little gatewayd-internal memory, nothing else.
