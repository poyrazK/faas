# ADR-700: Public ingress admission generations

Status: accepted · 2026-10-07

## Context

ADR-699 invalidates guard installation facts when reviewed membership changes.
It does not account for streams and upgrades already admitted under an earlier
review, or a blocked probe whose authorization returns after a review change.
Replacing the desired inventory cannot make that work disappear.

## Decision

Add default-off `FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY=1`, requiring the
ADR-698 guard on both forwarding paths, ADR-699 public confirmation and the
PostgreSQL generation store. Include the generation protocol in the startup
configuration fingerprint. Install one process-local tracker before exposing
the public listener. Begin an unknown/pending scope before dialing or probing.
Fresh authorization fences the internal head then public head, validates this
exact public slot/session/config and the receiving internal slot/session's
current heartbeat, and returns both roster revisions. Bind the scope to that
pair. No database transaction lasts for the forwarding lifetime.

Retain scopes through HTTP response-body Close and raw upgraded-connection
Close. Errors release scopes after closing the upstream. HTTP request cancellation
closes the upstream before completing its scope; the raw-upgrade caller closes
its established connection on cancellation. Dial-only contexts routinely cancel
on return and must not own an established upgrade's lifetime. Probe deadlines
do not shorten successful forwards. Preserve duplex response writers and trailers. Completion
is idempotent; concurrent streams remain independent. Late authorization binds
the original scope, so a current snapshot still sees older work until completion.

Use existing ADR-696 process bounds: 65,536 concurrent forwards and 4,096 active
generation pairs. On exhaustion, reject the private forward and permanently
lose known coverage for that startup session. Versions increase on begin, bind
and finish, never wrap beyond signed bigint, and exhaustion cannot recover by
eviction or completing later work. Counts contain no request or tenant data.

Publish activity immediately and every existing repair interval alongside guard
facts, within the same bounded poll context and listener lifecycle. Fence both
current heads, lock the fact table/row, then call a synchronous local snapshot
callback using the fenced revision pair. Callback performs no database IO.
Record exact public identity/config, enabled guard, monotonic activity version,
coverage flag and pending/current/previous counts under an exact one-minute
database-clock lease obtained after lock waits. Bound counts in Go and SQL.
Reject version rewind, changed counts at the same version and unknown-to-known
updates. Review clears these operational facts along with ADR-699 guard facts.
Register the new table as operational in the clone inventory.

Add a private `PublicEdgeControls.ObserveActivity` seam; no endpoint or CLI.
Require an explicit frozen public revision and unchanged internal revision,
read every declared edge's exact current process/config activity fact under both
head fences, and read the database clock after fact reads. Pending, previous,
unknown, missing, future, expired and subsecond facts block confirmation.
Current-generation work may remain active. Return `activity_observed` only
when every declared edge has known coverage without pending/older work; validity
is the shortest remaining fact lease. Report counts as bounded diagnostics.

This is a point-in-time observation, not a closed admission barrier or reusable
retirement lease. The begin-before-authorization ordering retains all work whose
old authorization could return late; current-head fencing prevents a new scope
from receiving an older authorization while publishing a new-generation fact.
New current work after the snapshot is permissible. A subsequent head change
invalidates the observation and requires another process snapshot.

## Consequences

Unreviewed public processes fail private admission rather than self-enrolling.
Same-process transitions conservatively retain previous-generation work, including
HTTP/1, H2C and raw upgrades, until it completes naturally. Desired inventory
still does not prove actual DNS/Caddy topology or the death/withdrawal of an edge
removed or replaced in a review. The new verifier describes only current declared
processes. Withdrawn-process barriers, native topology reconciliation and
scheduler/VM quiescence remain required for safe retirement; no cleanup writer
consumes this observation as authority.

All private flags remain disabled in deployment units; public execution remains
unavailable. No PR, push, production daemon, deployment, forced disconnect,
retirement or artifact deletion is authorized by this slice. Native Linux amd64
KVM `test-metal` and final `leakcheck` remain pending before enablement. Local
PostgreSQL and TCP/H2C fixtures establish control-plane/transport contracts only.

## Validation

Tracker contracts cover late authorization, multiple roster changes, independent
completions, invalid bindings, concurrent snapshots, saturation and sticky unknown
coverage. Transport contracts retain old HTTP/1/H2C bodies and upgrades, complete
on cancellation/errors and preserve the ADR-698 same-connection, duplex and
deadline contracts. PostgreSQL contracts check exact public/internal identity,
live heartbeat, all-edge coverage, older/pending/unknown counts, monotonic facts,
lease validity, review invalidation and locks/clock ordering. Startup checks cover
default-off, dependent flags/stores and versioned configuration fingerprints.

Local validation passed 66 focused test roots: 18 real-PostgreSQL state contracts,
16 public-proxy contracts, 16 ingress contracts and 16 private runtime-upgrade
contracts including their PostgreSQL subcases. All 32 ingress/public-proxy roots
passed under the race detector. Both host gateway binaries built without being
launched; production lint reported zero issues. The migrated clone inventory,
SQLC regeneration, eight static migration contracts and documentation/text/shell
gates passed. A read-only check found no migration-version collision among all
136 open pull requests. These are local contract results, not native acceptance.
