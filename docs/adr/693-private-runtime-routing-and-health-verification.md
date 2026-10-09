# ADR-693: Private runtime routing and post-cutover health verification

Status: accepted · 2026-10-06

## Context

ADR-689/604 completion proves atomic historical traffic activation. A notification
or database read does not prove a gateway applied that intent. An app marked
healthy may have no exercised requests, pre-cutover telemetry, or evidence from
another serving release. Neither signal justifies verified upgrade success.

## Decision

Retain the immutable operation phase `complete` as historical activation. Add a
separate account-scoped private `Controls.Verify` observation. It never changes
traffic, operation history, cleanup, rollback policy, scheduler or VM state.
There is no public API, Apply enablement, CLI or worker deployment change.

The private operator supplies a nonempty, unique, bounded set of gateway process
session UUIDs from a reviewed topology. There is no authoritative gateway fleet
membership registry today. Verification applies only to that explicit set and
cannot claim whole-fleet convergence. Each opted-in gateway generates a fresh
session on process startup; a replacement must be reviewed as a new participant.
Old sessions cannot inherit a restarted process's receipt.

`FAAS_RUNTIME_UPGRADE_ROUTING_CONFIRMATION=1` opts a gateway into receipt
publication and repair; the default is disabled. Install picker weights before
calling the receipt observer, outside the routing cache lock. Serialize each
app's read/install/publication sequence with a fixed set of context-aware lock
stripes, preventing older concurrent snapshots from replacing acknowledged
newer ones. Receipt publication failures are returned for retry while already
installed weights remain available. Gatewayd writes only its operational receipt
rows, never customer intent. Under the existing app traffic fence, acknowledge
only the exact positive 100-percent runtime candidate in the current production
routing lane with its immutable cutover. Stale cached weights after rollback
cannot be certified. Ordinary deployments create no receipt.

Receipts are tied to app, process session, candidate and exact cutover timestamp,
use the database publication clock and expire after one minute. Keep at most 64
fresh sessions per app, prune expired rows, and reset this operational table on
environment cloning. The opt-in repair loop starts immediately, polls every 15
seconds and handles one cursor page of at most 32 recently activated apps with a
10-second deadline. Refresh all entries before advancing the page; failure keeps
the cursor. Restart starts a new bounded scan. Pages wrap on exhaustion and may
require multiple polls; no receipt freshness or fleet convergence is assumed.
Only cutovers within the 30-minute operation horizon are repaired. Receipt
expiration leaves verification pending after that horizon. Shutdown cancels
in-flight polling. Repair-loop logs retain fixed messages, never database diagnostics.

Verification reads one repeatable-read PostgreSQL snapshot (or one MemStore
lock). Require completed matching activation history, the candidate still live
at explicit 100 percent, retained predecessor at zero (preserving its legacy explicitness marker), and no other
positive deployment in the production routing lane. Reconstruct the original
configuration, secret and source/input fingerprints after deliberately
normalizing only the expected traffic reversal; reject other drift. Require the
candidate artifact still bound to the exact unrevoked native qualified target.
Infrastructure errors propagate rather than becoming successful evidence.

Every reviewed session needs a nonfuture receipt for that exact cutover no older
than one minute. A fresh healthy default-scope collection must name only the
candidate as serving and latest, with confirmed ready capacity. Require the
entire existing five-minute request window to follow cutover, adding the
collector’s 30-second lease budget so the earliest query also follows cutover.
Require fresh nonfuture
metrics, confirmed deployment-scoped coverage, at least 50 observed requests,
valid consistent counts/rate, and an error rate below the existing five-percent
warning threshold. Count/rate consistency follows the producer’s integer-truncation
interval for fractional Prometheus increases. Zero traffic, parked capacity, stale evidence, wrong scope,
insufficient requests, degraded/unhealthy or unavailable telemetry remain
pending with fixed reason codes. Named environments remain unsupported until
health collection is scoped to them; default evidence is never borrowed.

Return the observation time, evaluated health time, reviewed session IDs,
confirmed count and conservative validity bounded by the oldest receipt,
assessment and telemetry sample. This is a point-in-time evidence result, not a
persisted success journal, reachability guarantee or exact incident history.
Later rollback, revocation, input drift or receipt expiry invalidates the next
observation without rewriting historical activation.

## Consequences

The private executor can distinguish activation from verified evidence using
real gateway installation receipts and existing health collection. Automatic
reviewed fleet membership, durable verification orchestration/deadlines,
connection drain and predecessor retention/cleanup remain subsequent work.
Fresh dedicated Linux amd64 KVM end-to-end acceptance, `test-metal` and final
`leakcheck` are still required before customer enablement; local synthetic
metadata proves no native runtime behavior.

## Validation

Contracts cover cache installation before receipt, serialized concurrent
refreshes, cancellation while waiting, failed receipt publication, lost-notify
repair, cursor retry, shutdown, account/app/session isolation, historical
activation after rollback, PostgreSQL current-input fences, scoped healthy
post-cutover verification, expired receipt pruning and clone schema coverage.
Evidence tables cover stale/future/mismatched receipts, scope, readiness,
telemetry freshness, observation windows, low traffic and elevated errors.
