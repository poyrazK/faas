# ADR-20261104123246: Pipeline initial runtime and CLI creation recovery

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261104105500, ADR-20261104115303

## Context

A prepared Data API app already has a mirror binding before its first deployment.
Schema refresh requested a scheduler restart for this app even when it had no
live deployment. The scheduler cannot restart an empty live set, while initial
API deployment waits for a fresh live pipeline. Schema changes during setup
therefore stranded both workflows.

CLI creation also checked source compatibility and an empty destination before
recovering an accepted pipeline. After a lost creation response, the instructed
retry with the retained database failed because replication had populated it.
Finally, a valid digit-led pipeline slug produced an invalid managed database
name when the CLI automatically provisioned its mirror.

## Decision

Stamp the runtime configuration freshness boundary and invalidate snapshots for
every bound app as before. Request a durable scheduler restart only when the app
has a live deployment. Otherwise report `not_required`, allowing schema completion
and the initial API deployment to proceed. Pending and failed deployments do not
constitute serving runtimes. Ignore an old unusable restart receipt while the
app has no live deployment. Live apps still require delivered restart evidence.

Replays retain the original freshness stamp and deterministic wake ID. If an app
becomes live during an unfinished workflow, enqueue that wake once without
restamping its boundary. Keep the existing pipeline lease fence, app limit,
explicit retry budget and scheduler ownership of runtime replacement.

Before CLI probes or mirror provisioning, inspect owned pipelines for the
requested name and fetch its current state. Recover only the same source,
table set and reader set, with the same mirror and region when explicitly
supplied. Compare UUIDs canonically and selections independently of ordering.
Reject deleted or conflicting intent. Metadata failures stop creation rather
than allocating replacement resources. Recovered pipelines use the ordinary
wait/output and optional Data API path, preserving the caller's deadline through
API setup. Fresh creations retain all source and empty-destination checks.

Preserve `NAME-mirror` for letter-led slugs. Digit-led slugs use
`p-NAME-mirror-db`, which starts with a letter, stays within the database name
limit and cannot collide with a letter-led slug's `NAME-mirror`. Derivation is
stable for retries and preserves the complete accepted slug.

## Validation and rollout

Catalog regressions cover absent, pending, failed and live deployments; freshness
stamps; snapshot invalidation; late runtime creation; stable wake replay;
delivered acknowledgements; and legacy dead letters. Native TLS replication
acceptance covers an approved schema addition with an undeployed bound app,
continued CDC and initial API deployment admission after schema completion.
HTTP CLI regressions simulate an accepted creation whose reply is lost, blocked
subsequent probes, exact-intent recovery and mismatched selections. Naming
coverage includes digit-led and maximum-length slugs, stable retries and names
that would collide under prefix-only derivation. Full pipeline acceptance, race
detection, generated SQL and affected-package lint remain required.

No new catalog migration is required. The outbox schema addition is a read-only
SQLC catalog projection; the production outbox already exists. Apply the preceding
ownership migrations before deploying the combined changes. Production and
provider qualification gates remain unchanged.
