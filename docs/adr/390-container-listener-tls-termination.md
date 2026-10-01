# ADR-390 · TLS termination for container TCP listeners

- **Status:** proposed; implemented locally, native qualification incomplete
- **Date:** 2026-09-30

## Context

Raw TCP endpoints currently forward encrypted or plaintext bytes unchanged.
HTTP TLS belongs to the upstream Caddy/Cloudflare boundary. Terminating TLS for
an arbitrary container protocol needs a separate explicit listener contract;
an HTTP reverse proxy cannot implement that contract for arbitrary byte streams.
This extends the public edge's responsibilities and therefore requires an ADR.

## Decision

Add an explicit listener TLS mode, defaulting to passthrough for existing and new
listeners. Termination is opt-in and occurs only in gatewayd-public. apid owns
listener intent, schedd owns admission, and vmmd continues forwarding plaintext
to the configured guest TCP port. Neither schedd nor vmmd receives certificate
private keys. HTTP TLS and UDP framing retain their existing contracts.

Termination requires a hostname owned by the listener's app and a usable
certificate supplied by a public-edge certificate provider. Provider lookup is
an interface that returns an immutable certificate snapshot; it must support
rotation without closing the listener. Do not assume that gatewayd-public can
read Caddy's private storage. The deployment adapter must explicitly provision
the certificate provider and its read permissions. Missing, expired, incorrectly
named or otherwise invalid certificates fail closed without waking the app.
Never silently fall back to passthrough or plaintext.

The first implementation supports server authentication and a single hostname
per endpoint. Client certificates, ALPN-dependent routing and protocol-specific
STARTTLS require separate contracts. Require a matching SNI hostname; the public
port is not a tenant-identity substitute. Use TLS 1.2 or newer with Go's secure
defaults. Customers receive certificate readiness/expiry metadata, never keys.

Acquire global and account session credits before the handshake. The handshake
has its own deadline and is canceled by listener disable, reassignment or gateway
shutdown. Clear that deadline after success, then apply existing admission,
idle, byte and lifetime limits. Certificate lookup and handshake happen before
scheduler admission so unauthenticated clients cannot wake workloads. Resource
limits belong in pkg/api/limits.go. Handshake failures release every credit and
join all connection goroutines.

## Implementation sequence and invariants

1. Add a portable handshake seam with an injected certificate provider, strict
   SNI/name/expiry checks, bounded cancellation and socket tests. Keep it unused
   by production until the remaining layers are wired.
2. Append a migration for TLS mode and hostname. Add shared API validation,
   state projections and authenticated API/CLI configuration. apid validates
   current app domain ownership on configuration and enable; the edge rechecks
   intent and ownership before admission. Legacy rows remain passthrough.
3. Include TLS identity in supervisor reconciliation. Mode or hostname changes
   cancel old sessions before rebind. Certificate rotation refreshes snapshots
   for new handshakes; established sessions retain their negotiated connection.
4. Add the certificate deployment adapter, opt-in environment contract, least
   privilege access and bounded metrics/alerts. Certificate readiness is distinct
   from listening-socket readiness. Preserve normal TCP drain behavior.
5. Qualify full API/CLI-to-edge operation on local sockets and real PostgreSQL,
   then native cold wake/restore and leak acceptance. Verify invalid SNI, expired
   and rotated certificates, stalled handshakes, quota contention, maintenance,
   disable/reassignment, node failure and gateway drain. Native qualification
   remains pending while no acceptance host is available.

## Consequences

Customer certificate status uses observations written by gatewayd-public to a
separate table keyed by listener and edge identity. apid remains the sole writer
of listener intent. Observations carry the normalized hostname, intent revision,
observation time, readiness and certificate expiry; no key, path or provider error
is persisted or returned. Readiness describes each observed edge, never presumed
fleet coverage. Missing evidence, policy revision mismatch, disabled/passthrough
intent, future timestamps or evidence at least sixty seconds old produce unknown
status. Certificate expiry since observation produces not-ready status. Retain
one latest observation per listener/edge, reject older updates, and prune stale
rows so retired edge identities do not accumulate. API reads must authenticate
listener ownership before exposing this evidence. PostgreSQL and MemStore now
implement publication, latest-per-edge lookup and stale-row pruning. The SQL
writer conditionally accepts only current enabled TLS intent and monotonically
newer evidence. Shared race-tested contracts pass on both stores, including
revision conflicts, replacement, independent edges, pruning and listener-delete
cascade. The full migration chain and generated sqlc consistency check pass.
The public edge now publishes readiness changes during reconciliation and
refreshes unchanged evidence every fifteen seconds, under a two-second cycle
budget. Local publication cache entries disappear when listeners disappear;
stale durable evidence is pruned periodically. Publication failures are reported
without replacing listener reconciliation failures or altering handshake policy.
Production uses `FAAS_NODE_NAME`, with `legacy-singlebox` for the existing unnamed
single-node posture. Race-tested socket composition observes missing-bundle,
ready and disabled status through durable evidence; publication tests cover
heartbeat throttling, immediate changes, cancellation and retry. The authenticated
listener-specific status endpoint and `apps tcp APP tls-status NAME` now expose
only edge identity, status, observation time and current expiry. API race tests
cover authentication, app/listener ownership, empty evidence, ready/disabled state
and stale-expiry suppression. HTTP-backed CLI race tests cover request identity,
human/JSON output and argument rejection. OpenAPI route/DTO parity and lint pass;
native publication-to-API qualification remains pending.

Customers can use TLS with protocols whose applications expect plaintext inside
the guest. The public edge gains certificate availability and handshake CPU
costs, which must be bounded and observable. Certificate provisioning is a
required part of the feature; a handshake helper alone is not completion.
Existing TLS passthrough continues to work without certificate-provider access.

## Current implementation evidence

`pkg/tcpd.TerminateTLS` now supplies the portable seam with an immutable provider
contract, TLS 1.2 minimum, exact listener SNI matching, certificate name/validity
checks, a shared ten-second handshake bound and cancellation cleanup. Race tests
cover success, missing/wrong SNI, wrong certificate identity, expiration and a
stalled client canceled by its parent context. It is not wired into production;
durable configuration, provisioning, rotation integration, credit accounting,
operator readiness and native acceptance remain required.

The TCP server now accepts an explicit route TLS hostname and certificate
provider, reserving global/account credits before negotiation and resolving the
workload only after success. A local socket regression rejects invalid SNI with
zero admissions, requires credit release, and then obtains an encrypted reply
from the plaintext forwarding seam. Production routes do not yet populate the
TLS hostname; durable configuration and the provider deployment remain pending.

The shared API policy now defines `passthrough` and `terminate`, normalizes DNS
hostnames and rejects incompatible mode/hostname combinations. Termination
rejects IP addresses, wildcard names, empty/oversized labels and non-ASCII names
unless supplied in DNS punycode form. The handshake seam consumes this same
validation. This policy is not yet exposed through listener request DTOs, so the
API cannot silently accept TLS configuration before persistence is implemented.

TCP state now carries TLS mode/hostname through PostgreSQL inserts and all
listener projections, with the same normalization in MemStore. An append-only
migration defaults existing rows to passthrough and constrains mode/hostname
combinations. Portable store tests verify defaults, normalization and rejection;
a PostgreSQL round-trip regression is added but has not yet executed. The target
resolver rejects a cached plaintext route when durable intent requires TLS,
preventing partial rollout from silently forwarding plaintext. UDP has its own
state structure and does not inherit TCP TLS fields. Customer API configuration,
domain ownership and actual PostgreSQL migration acceptance remain pending.

PostgreSQL 16 has now applied the full migration chain through the TLS migration
in isolated schemas. `TestPgStoreTCPListenerTLSRoundTrip` and the existing TCP
listener lifecycle regression both reported PASS without skips. The new case
verifies normalized termination hostname/mode and legacy passthrough defaults
through actual storage reads. The task-owned temporary cluster was stopped after
the run. Direct SQL constraint pressure, API domain ownership, certificate
provisioning and native acceptance are still outstanding.

The public-edge target resolver now rechecks TLS hostname ownership using current
custom-domain state before instance selection or admission. A shared state
validator requires the exact normalized hostname, matching app, completed TXT
verification and no environment binding. Environment-bound domains require a
separate scoped-admission contract. Resolver race tests reject missing,
unverified, foreign, mismatched and environment-bound domains without waking an
app, and admit a verified app-wide domain. apid configuration checks still need
to consume this validator when TLS request fields are exposed.

Listener creation and responses now expose structured TLS intent. apid validates
normalized policy and current verified app-wide domain ownership during creation
and rechecks ownership on enable. Termination listeners start disabled;
passthrough preserves existing creation behavior. These API changes do not imply
certificate readiness: provider provisioning and public-edge route wiring are
still required before a termination endpoint can serve traffic. Mode/hostname
updates and CLI configuration remain unfinished.

CLI creation now accepts `--tls-mode terminate --tls-hostname HOST`, using shared
normalization before any HTTP mutation. Human listings show TLS mode/hostname,
and creation reports the disabled endpoint plus certificate provisioning step.
HTTP-backed CLI regressions verify normalized intent, authenticated endpoint,
disabled output and invalid-input rejection without requests. API lint and the
CLI cases pass locally. TLS mode/hostname updates, certificate provisioning and
production route propagation remain unfinished.

Durable route lookup and supervisor reconciliation now share a validated
listener-to-route projection that preserves TLS hostname. Supervisor-managed
servers receive the injected certificate provider, and hostname changes
participate in route identity comparison and session cancellation. The complete
TCP race suite passes after this wiring. Production certificate-provider
configuration remains absent, so enabled termination intent fails closed at the
handshake instead of forwarding plaintext. Provider deployment and integrated
rotation/reconciliation acceptance still need implementation.

A file-backed public-edge provider now reads `<hostname>.pem` bundles under an
`os.Root`-anchored directory. A single PEM includes the chain and matching private
key; operators replace it by atomic rename, and each handshake receives a fresh
immutable snapshot. Reads are capped at 64 KiB, public permissions and group write
are rejected, and nonblocking open prevents a malformed FIFO from holding a
session before the regular-file check. Portable race regressions cover rotation,
snapshot stability, permissions, size, cancellation, missing names and root
escape; a Unix FIFO case checks bounded rejection. Production environment and
deployment permissions still need wiring, so this provider is not yet active.

Production gateway startup now opens the optional `FAAS_TCPD_TLS_CERT_DIR`
provider, injects it into the supervisor and closes it after sessions stop.
An unset directory preserves passthrough; a relative/unavailable configured
directory fails startup. The Ansible setting defaults empty and prepares an
explicit directory as root:faas 0750, leaving certificate issuance and atomic
bundle provisioning to the operator. The environment registry and generated
documentation are synchronized. Startup configuration tests, environment gates
and changed-code lint pass locally; integrated certificate readiness/rotation
and native acceptance remain pending.

`TestTLSIngressRotationAndDisable` now composes real public TLS sockets, durable
in-memory intent/domain state, supervisor reconciliation, target selection and
the file provider. Clients verify the hostname/certificate using a test trust
pool. Atomic rotation changes the certificate presented to new clients while an
established session continues, a second peer reuses the running instance, and
disable closes both sessions and permits rebinding the public port. Three race
repetitions pass. Scheduler admission and guest execution are substituted; this
does not establish native cold wake/restore or production certificate issuance.

TLS policy updates now have a dedicated optional store interface. PostgreSQL
updates mode, hostname and `enabled=false` in one statement; MemStore mirrors
that mutation. The API accepts either serving-state or TLS-policy PATCH, rejects
mixed mutations, validates ownership and leaves changed listeners disabled for
provisioning/re-enable. Portable state and PostgreSQL 16 regressions verify the
atomic disable and passthrough transition; the temporary cluster is stopped.
CLI updates and native reconciliation acceptance remain unfinished.

CLI updates now use `apps tcp APP tls NAME --tls-mode MODE [--tls-hostname HOST]`.
The command requires explicit policy, shares hostname validation, sends a TLS-only
PATCH and reports the disabled result. HTTP-backed CLI tests verify normalized
intent, no enable field, the listener-specific path and missing-policy rejection.
Native policy-transition/session cancellation qualification remains outstanding.

Reconciliation now publishes aggregate certificate-ready/unready listener counts
and earliest certificate expiry, using the same hostname/validity checks as the
handshake. Missing bundles leave socket readiness distinct from certificate
readiness; disable and shutdown clear observations. The integrated TLS test
starts without a bundle, requires unready state, provisions a certificate and
requires readiness, then verifies reset on disable. Race execution passes after
a disk-space build failure was retried. Customer-facing status and alerting still
need integration; these gauges do not prove guest or domain-route availability.

The Prometheus deployment now includes separate unavailable-certificate and
seven-day-expiry alerts with five/ten-minute persistence windows. Expiry requires
a positive ready-listener count. `make tcp-tls-alert-check` validates all 241
rules and scenarios for firing, persistence delay, healthy certificates and
disabled endpoints. Customer-facing certificate status and native acceptance
remain unfinished.

Native `TestTCPIngressMetal` now includes API-driven passthrough-to-TLS policy
transition, disabled configuration, verified fixture domain ownership, trusted
certificate echo through the actual VMMD guest transport, and a fresh wake ID
with durable `restore` evidence after parking. It provisions a temporary edge
bundle through the production directory setting. The raw echo expectation also
now counts the fixture prefix once. Final-source Linux/amd64 metal compilation
passes, and phase contracts still select the test; native execution and leak
acceptance are not performed without an acceptance host. Fixture domain
verification is seeded, so this test does not prove external DNS verification.
