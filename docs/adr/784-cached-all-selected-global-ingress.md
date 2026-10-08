# ADR-784: Cached ingress across selected regions

Date: 2026-10-08
Status: Implemented locally; live directory publication and full regional acceptance pending.

## Context

ADR-730 records global app identity and the customer's `all_selected_regions`
policy. Regional apps remain independent authorization, wake and quota owners.
There is no global traffic consumer, and a management API origin is insufficient
to address an app's regional data plane. Cached ingress must not manufacture
runtime eligibility or turn stale directory observations into native permission.

## Decision

Add an opt-in projection consumer in `gatewayd-internal`, reached through the
existing `gatewayd-public` listener. It wraps both the unified Unix handler and
the legacy listener handler. The actual database must be unbound; a regional
database is refused before the handler is installed. Manifest validation also
refuses this configuration in a regional cell or on another daemon. No second
public listener or direct scheduler/VM owner call is introduced.

The frontend serves `<global-app-uuid>.<global-ingress-apps-domain>`. The global
domain must be disjoint from the local apps domain, including suffix overlap.
App UUIDs avoid cross-tenant slug collisions. Other hosts retain existing routing.
Recognized malformed global hosts and missing global apps return 404 rather than
falling through to private control routes. A stale/unverified projection or an
app with no usable selected target returns 503. A request already carrying the
global-hop marker returns 508.

An immutable whole-directory snapshot names the directory UUID, monotonically
increasing generation, issue/expiry microseconds, exact all-selected policy,
global app UUIDs, selected regional bindings and separate HTTPS app origins.
Each target has publisher-observed healthy and eligible flags. Both must be true.
Duplicate app identities, regional bindings, selected regions and selected cells,
noncanonical origins and oversized payloads are refused. A lease lasts at most
30 seconds. Bounds are centralized in `pkg/api/limits.go`.

Existing persistent Ed25519 directory trust anchors verify a distinct signed
`global-ingress` message kind and expected directory UUID. A cell key cannot
publish routes. Domain separation prevents a route snapshot from becoming a
runtime quota grant; existing quota payload/envelope bounds remain unchanged.
The larger route envelope has its own 16 MiB payload and 24 MiB envelope bounds,
10,000 apps and 32 selected targets per app. Claims never authorize launches,
acknowledge family absence, reconcile capacity, release holds or authorize billing.

The protected regular handoff file is read at startup and once per second on a
background worker. Reads refuse symlinks, FIFOs, hard links, writable group/other
modes and oversize inputs. Publishers must atomically rename complete files.
Parsing/signature verification runs outside the selection lock; cache replacement
is atomic. A failed refresh quarantines the source immediately. Old generations
and changed payloads at an existing generation are rejected. Identical retries
are accepted. This generation watermark is process-local, not durable anti-replay
across restarts; signed lease expiry still bounds every accepted projection.

Every healthy eligible selected target participates in round-robin selection.
Lease renewal retains the cursor and exact-target failure cooldown. A transport
failure excludes that target for five seconds for subsequent requests. The failed
request is never replayed in another region because its first attempt may already
have performed an effect. Caller cancellation does not mark a region failed.
HTTP status alone does not declare regional health. There is no implicit primary.

The proxy verifies ordinary HTTPS, rewrites to the exact regional app origin,
preserves app authorization/cookies, query/body and streaming/upgrade transport,
and clears reserved platform identity and invocation-source headers. It derives
forwarding headers from its direct connection rather than accepting caller claims.
Regional gateways continue to own app access, wake and quotas. The existing
outer public edge owns request IDs and static security headers; app CSP remains
intact. Shutdown joins the refresh worker and closes idle upstream connections.

## Integration still required

This change implements the consumer and daemon wiring, not an operational route
publisher. APID must produce live snapshots from current directory selection,
owner consent, cell generation, regional serving evidence, admission and billing
eligibility. It needs an independently configured app data-plane origin per cell;
the existing management API origin must not be guessed into an app origin.
The consumer trusts that signed assertion and does not independently prove health
or native eligibility. No live route is published or regional admission activated.

Shared authorization/billing federation, tenant sessions, original-host redirects
and cookies, trusted client-IP propagation through both public boundaries,
regional infrastructure/stores and secret/data replication remain work for the
complete feature. Regional token, origin and host behavior is retained until
those contracts are implemented. Do not advertise host or session transparency.
Native inventory/ownership, first-fence capacity, snapshot capture/pause/migration,
immutable-input latency and dedicated native x86_64 KVM metal/leakcheck/fleet
recovery acceptance remain required. A short-lived signed route is no substitute.

## Validation

Portable tests exercise two real TLS regional servers, all-selected distribution
through lease renewals, forwarding/header/query/body behavior, authorization
passthrough, eligibility filtering, cache expiry/withdrawal/quarantine, generation
rollback/equivocation, concurrent selection, failure cooldown, no cross-region
replay and caller cancellation. Separate tests cover signed authority, kind and
legacy quota isolation, protected file refresh/cancellation, configuration and
database scope, manifest placement and rendering. These are consumer diagnostics;
they do not qualify regional runtime or native recovery acceptance.
