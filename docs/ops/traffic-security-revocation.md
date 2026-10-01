# HTTP security revocation

ADR-375 separates a request's immutable traffic policy from emergency security
state. Postgres `traffic_security_epochs` records account suspension/deletion
pending and abuse holds, app deletion, and deployment security quarantine.
Every revoke and release advances a generation. Physical deletion retains a
tombstone without a cascading foreign key. Plan, ordinary configuration,
traffic weights and nonsecurity parking changes do not advance generations.

## Gateway behavior

Both production gateways verify the table with a bounded read before
serving. Compute's public HTTP handler enrolls its owner account and app before
authentication, upload and wake, then its selected deployment before dispatch. A retry checks
each additional deployment. Edge route substitutions also enroll the verified
source app before wake; deleting that app cancels the routed target exchange.
The source scope remains registered until forwarding cleanup finishes, and
public response ownership verifies it with the other admitted scopes.
Service calls enroll the verified caller app and,
when available, its source deployment before discovery; account and target app
follow tenant authorization, before wake. Each selected target deployment is
checked before forwarding. Identity comes from routing, tenant authorization
and node source-instance resolution, never an arbitrary scope header.

Normal managed synthetic HTTP uses this registry as well. The gateway verifies
the saved account, app version and every claimed running instance/node/scoped
live deployment in one committed view, including unpinned invocations. Account
and app enroll before gateway-owned wake, and the deployment before forwarding.
A nested delivery shares that lifetime through cleanup. Trigger batches carry
the trigger's saved account to every record. Older trusted account-free envelopes
keep compatibility; the verified app supplies the security account scope.
Production schedd verifies this store before lifecycle startup, then runs bounded
periodic repair. Its durable invocation drain enrolls account/app before wake
and a selected pinned deployment before its wake. Unpinned delivery adds the
verified returned deployment. The drain rechecks the exact admitted generations
after wake and before recording success. A cached account allow cannot bypass
these checks. Cancellation stops the delivery's wait; the existing coordinated
wake leader retains its independent bounded lifecycle for other callers. Pinned
wake uses the delivery context. Claim failure/retry writes use the original
scheduler context so a canceled delivery does not strand a writable claim.
Registrations remain owned through gateway result handling and cleanup.

The trusted single-dispatch JSON body carries `security_snapshot`, using the same
canonical `v1.` codec and 4 KiB/16-scope bounds as public response handoff. The
receiving gateway requires the exact resolved account/app/target scope set and
checks the sender's generations before forwarding. A missed revoke/release pair
refuses the old delivery even if the account is active again. Guest headers cannot
author this field; it is not persisted as customer configuration. Malformed
metadata, missing owner storage or an unwired registry refuse enforcement.
Absence keeps older trusted callers compatible. Debug mirror replay
retains its separate mirror target owner and legacy scheduler account gate. This does not apply public edge rules
or public request rate accounting to background work.

An independent lifetime fence survives successful streaming/gRPC/Upgrade
handshake detachment. Changing any enrolled generation cancels the exchange,
including a revoke/release pair missed between reads. Store failure or
inconsistent/regressed generations also cancel active tracked exchanges and
refuse new admission. A warm policy snapshot supplies no security allow lease.

The protected compute response carries its exact admitted generations in
`X-Faas-Traffic-Security`. Public verifies the same baseline before committing
the response and independently tracks those scopes. It never replaces compute's
baseline with a newer released generation. This read is bounded to 250 ms and
the transport header to 4 KiB/16 scopes. Invalid, ambiguous or missing metadata
refuses a successful application response with 503; a changed generation gives
403 `traffic_revoked`. Pre-admission errors may have no owner snapshot. The
separate managed realtime handler explicitly marks its excluded surface.
Neither guest headers, edge header actions nor late trailers can author the
snapshot, and it is removed before customer delivery.

Public refreshes active scopes once a second, without depending on compute's
notifications or reading its next response bytes. A blocked HTTP/1 socket write
or HTTP/2 flow-control wait is interrupted on cancellation. Successful Upgrade
tunnels retain an independent 24-hour ceiling, close both client and compute
sockets on cancellation, and join both copy goroutines before unregistering.
Forwarding ownership and node permits remain until forwarding cleanup finishes.
Final handler cleanup unregisters the scopes after stopping response-write
guards, avoiding a late cancellation of the HTTP server's buffered flush.

`traffic_security_changed` is a wake-up to re-read authoritative rows, and
refreshes advisory route/app flags. Its JSON identifies scope kind, UUID and
generation; it is never treated as an allow/revoke instruction. Each registry
also reads active scopes once a second, with a 250 ms store-operation timeout,
so missed notifications do not leave an old exchange alive indefinitely.

Limits are in `pkg/api/limits.go`: 65,536 simultaneous registry registrations,
4,096 distinct active security scopes and 16 distinct scopes per request across
all attempts. Account/app and later deployment enrollment may use separate
registrations, so the registration bound is conservative for logical requests.
Capacity refusal includes the actual limit, observed count and documentation
URL; it does not silently skip tracking.

## Responses and recovery

Before headers, emergency cancellation returns `traffic_revoked`/403. Known
initial account holds and suspension preserve their existing problem contracts.
Verification failure returns `traffic_revocation_unavailable`/503 and
`Retry-After: 1`. Error delivery has a separate bounded 100 ms write allowance.
After headers, the forwarding transport aborts instead of appending a problem
document or completing a partial success body. HTTP/1 and HTTP/2 write guards
also interrupt a blocked compute-side response writer.

A released generation allows a fresh request. Cached old account hold/status
flags cannot override a verified release. Existing app security-quarantine
admission still refuses before wake, and notifications refresh its route facts.
Deleted identity tombstones and released generations survive gateway replacement.
Individual credentials, managed realtime, other detached work and arbitrary guest
sockets remain outside this initial account/app/deployment fence. Durable normal
invocation delivery is covered as described above; guest effects already performed
are not rolled back by cancellation. Refused or unverifiable attempts use the
existing finite invocation retry budget. A later attempt enrolls a fresh lifetime.

## Rollout and local evidence

Apply `20260929230709001_traffic_security_epochs.sql` before rolling internal
gateways or schedd. A missing migration or unreadable store refuses their startup.
Roll updated synthetic consumers before schedd producers, drain invocation dispatch
during the cutover and resume on matched versions. Older consumers ignore the
optional JSON field and cannot enforce the sender's baseline. Older producers
leave scheduler-owned wake outside this fence. This is an operator rollout contract;
it does not claim automatic mixed-version capability negotiation.
Update every participating internal gateway and both its public-request and
service-proxy listeners before advertising fleet coverage. The public proxy
response/session/security metadata rollout remains compute-first, as described in
`traffic-total-deadline.md`. Drain public ingress during the compute/public
cutover: an older public proxy can forward an unfamiliar private response
header, while the enforcing public version refuses successful responses from
older compute. Resume ingress on the matched versions. Older gateways do not
provide this lifetime fence.

Local tests cover account/app/deployment cancellation, initial refusal, upload
spool removal, wake cancellation with real HTTP/1 and HTTP/2 error delivery,
public and service retry-deployment checks, scope capacity, cleanup ownership,
and real gRPC cancellation after successful handshake-budget detachment.
Synthetic endpoint tests verify every target claim, before-wake refusal,
account/app/deployment cancellation, missed revoke/release pairs, store failure,
late-success rejection and registration ownership through forwarding cleanup.
Scheduler drain tests also cover initial warm/cold refusal, cancelable pinned
wake, canceled waiters with an independently finishing shared leader, retained
registrations through forwarding cleanup, exact checks after wake and before
success, late-result refusal, and durable retry outcome writes. The real schedd
factory, PostgreSQL store and HTTP producer emit the admitted baseline and persist
a valid completion. Its VMM and receiving gateway are fixtures. Startup tests
refuse a missing migration or closed pool before lifecycle work. A separate actual
synthetic HTTP endpoint with independent PostgreSQL sender/receiver registries
refuses a stale handoff after an unobserved suspension/release pair and accepts a
fresh baseline; registrations and database connections release after delivery.
Two synthetic HTTP endpoints with independent PostgreSQL pools/registries verify
missed suspension/release without notifications, fresh recovery and a one-pool
outage while the peer continues. They share a test process and fixture forwarding;
they do not establish deployed fleet or native VM/network acceptance.
Blocked ordinary and streaming compute response writes use a 64 MiB body and
an open unread client over HTTP/1 and HTTP/2; both handlers and upstream RPCs
must finish before the test closes the client.

Two real HTTP service gateways against Postgres, with notifications deliberately
absent, canceled a missed hold/release pair through periodic repair. Closing
only one gateway's database pool canceled that gateway and refused its new
traffic while the healthy peer continued. A replacement verified the durable
released generation. These are gateway instances in one test process; source
identity and forwarding ownership are fixtures. The separate gateway tests use
real gRPC transports. Public-hop ordinary and long response tests now also hold
64 MiB HTTP/1 and HTTP/2 clients open and unread: public, compute and the real
upstream RPC finish after public-only security refresh, before client closure.
Revoke, a missed revoke/release pair, store failure and periodic repair without
notifications are covered. Handoff tests refuse an intervening revoke/release
and accept a fresh request. HTTP/1 Upgrade fixtures cover idle tunnels and both
blocked directions; cancellation ends both socket owners before the client is
closed. Metadata refusal, guest/late trailer forgery and registration cleanup
are covered across ordinary and rejected-upgrade responses. These public
transport tests use an in-memory security store; Upgrade's compute endpoint is
a fixture. Source-route cancellation tests use real HTTP/1 and HTTP/2 clients
and keep all four account/source/target/deployment registrations until joined
forwarding cleanup. The security store and forwarding owner are fixtures.
Full daemon, native
VM/network/leak, deployment and complete policy-path acceptance remain pending.
