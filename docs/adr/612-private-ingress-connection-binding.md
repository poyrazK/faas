# ADR-612: Private public-edge connection binding

Status: accepted · 2026-10-07

## Context

ADR-609 reviews internal gateway processes; ADR-611 observes their fenced HTTP
forwards. A public edge can still send traffic through a cached compute gateway
pool, a static TCP target, or a local socket. The current compute registry does
not prove which process receives a request on one of those connections. A probe
on a separate connection cannot bind the eventual forward, especially across a
restart, failover or a refreshed pool.

## Decision

Add the default-off private `FAAS_RUNTIME_UPGRADE_INGRESS_CONFIRMATION=1` flag on
both gateway daemons. Provision the same random 32-byte secret, encoded as
canonical lowercase hex, in `FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN`. Do not enable
either flag in a deployment unit, register a customer endpoint, expose a public
Apply action, or start a production daemon as part of this slice.

The internal process requires routing and drain confirmation before constructing
its identity handler. Its stable slot and fresh process session are the same
identities used by ADR-609/611 and its process-wide admission tracker. Install the
handler only on the protected public-to-internal forwarding listener, shared by
the Unix and optional private TCP paths. Do not install it on guest service
listeners or the public control mux. The private infrastructure Host and exact
path identify the probe; the same path on customer app hosts remains owned by
the app. Invalid credentials, method, query, or nonce return 404.

Use a fresh canonical random nonce for each connection. The request carries an
HMAC-SHA256 proof over its nonce with a versioned request domain. The reply signs
the nonce, canonical slot, canonical session, and enabled admission fence under
a distinct response domain. Compare proofs in constant time. Never transmit the
shared secret, put it in a URL, include it in the reply, or log it. Require a
bounded JSON body with an explicit length, known fields, exact echoed nonce,
valid response proof, canonical identities and the enabled fence. Reject trailing
JSON, malformed, oversized, unfenced or replayed replies. Do not follow redirects.

After authenticating the receiver, perform a fresh PostgreSQL-only membership
read in a repeatable-read transaction. Require its exact slot/session pair in
the current reviewed roster and a matching nonfuture heartbeat with at least
one second remaining in its exact one-minute lease. Check the database clock
after evidence reads so a read wait cannot extend liveness. Do not discover,
enroll, replace or remove members, cache authorization, or substitute compute
node liveness. Return an observation of the current binding, never a retirement
lease. Existing review and heartbeat writers are unchanged.

On every private public-edge forward, obtain a connection through the actual
configured dialer, including the database-backed pool and its real cache/fallback
behavior. Authenticate and authorize the receiving process on that SAME
connection before handing it any customer body. HTTP/1.1 uses a fresh private
transport whose dialer can hand out only that connection; retry cannot switch to
a different process. H2C uses one client connection for the identity and actual
streams. Private forwards do not reuse a connection or authorization from a
previous request. The HTTP/1 upgrade path probes its connection directly before
the standard proxy owns the bidirectional tunnel; it cannot redial between proof
and upgrade. Reject buffered bytes beyond the probe response on that handoff.

The two-second bound covers dial, identity exchange and membership lookup only.
Cancel or failed proof closes the connection. On success remove the probe
deadline and cancellation hook before forwarding. Preserve the customer request
context, duplex streaming, response trailers, original transport timeout policy
and the upgrade lifetime. Release private transports with their response body,
including the writer interface on a 101 body. A pre-forward denial closes the
unforwarded request body. Strip proof headers from ordinary customer forwards.
Map unavailable/unreviewed bindings to the existing sanitized 503 response;
never fall back to the unguarded path. A failed binding on a selected pool peer
does not silently remove that participant or infer coverage of the remaining set.

Bounds live in `pkg/api/limits.go`: two-second probe, 1 KiB identity body, 32-byte
secret, existing response/header envelope and 64-member roster. No schema,
customer DTO, instance state or deployment mutation is added. The store interface
is additive and PostgreSQL-only; test doubles cannot mint production authority.

## Consequences

This closes the unchecked receiving-process gap for one explicitly configured
public edge. It does not prove that every Caddy/DNS/public listener is configured
to run the guard, publish a whole-fleet ingress coverage receipt, fence a roster
review against an already authorized forward, or bind traffic from another
entrypoint. Native public-fleet inventory and configuration evidence remain
required. Existing in-flight work can retain the binding it was admitted under;
future coverage/retirement publication must account for it and fence transitions.

Raw TCP/UDP services, synthetic callers, private guest services, legacy/direct
vmmd callers and scheduler/VM-owned work are outside this connection guard.
ADR-611's forwarding drain and the remaining scheduler/VM quiescence checks
remain necessary. Neither this fresh authorization nor historical verification
allows predecessor retirement, artifact deletion or automatic rollback. Public
execution remains unavailable. No PR, push, deployment or customer traffic is
part of this change. Native Linux amd64 KVM `test-metal` and final `leakcheck`
remain pending before enablement; local TCP/H2C/PostgreSQL fixtures are synthetic.

## Validation

Real TCP HTTP/1.1 and H2C contracts check one connection per forward, fresh
membership, receiving-process replacement denial before customer forwarding,
no secret transmission, proof-header handling and response trailers. Real
duplex upload/response and upgrade echo contracts retain both directions.
Cancellation covers a blocked identity exchange and blocked membership check;
a forward outlives the probe bound. Concurrent H2C forwards own independent
connections. Codec contracts reject tampered, replayed and malformed evidence
and preserve customer-owned paths. Public proxy contracts check default-off
wiring and the actual HTTP/H2C/upgrade seams with sanitized rejection. Internal
flag contracts require the existing private drain/routing configuration.
PostgreSQL contracts require current membership, reject missing/future/expired
or subsecond liveness and restarted processes, and expire a heartbeat during a
blocked read. Existing roster, drain and installed-weight contracts remain
dependencies. These tests are not whole-fleet or native retirement acceptance.
