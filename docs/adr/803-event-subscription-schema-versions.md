# ADR-803: Application subscription schema version selection

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Capture an optional list of accepted schema versions with each application event recipient.
- **Why:** Producers need to introduce a new envelope schema while consumers upgrade independently.

## Selection and capture

Subscriptions may select up to 16 unique, case-sensitive identifiers, each
1–64 ASCII characters starting with a letter or digit and containing letters,
digits, dots, underscores, or hyphens. Selection does not require an existing
schema registry entry, allowing consumers to prepare before a producer rollout.
An empty or omitted selection accepts every version, including unversioned
historical envelopes. A nonempty selection excludes unversioned envelopes.
Source and type matching precede version selection; content filtering follows it.

Publication captures the selection with each recipient. Configuration changes
apply to future publications. Historical backfills capture the current selection
at job creation; retained replay previews use the current selection and include
it in their continuation revision. Existing captured recipients without this
field accept every version. Workflow and object notification routing retain
their existing matching behavior.

## Filtered outcomes

A captured incompatible version settles as `filtered` with
`filter_reason: schema_version_mismatch`. It creates no invocation, consumes no
routing retry attempt, and contributes no circuit breaker failure. It is filtered
before expiry, backoff, pause, circuit, or ordering gates. Active leases remain
fenced. Claim completion restores the counters temporarily incremented by claim
acquisition. Receipts and bounded history retain the filter reason.

## Configuration surfaces

The `/v1/apps/{slug}/event-subscriptions/{subscriptionID}/schema-versions`
resource supports GET, PUT with `{ "schema_versions": ["v1", "v2"] }`, and
DELETE to accept every version. PUT requires an array; an explicit empty array
also resets the selection. Read and deployment write scopes, account/app
ownership, MFA, bounded requests, and auditing follow other subscription controls.

Manifests declare `schema_versions` on `triggers.event` entries. Omission resets
selection. Deployment compensation restores an existing declaration only if its
selection still equals the value applied by that deployment. Subscription
identity remains source/type/filter, so changing selected versions updates the
same consumer. CLI and generated Go, Node, and Python client surfaces expose
the configuration and preview diagnostics.
