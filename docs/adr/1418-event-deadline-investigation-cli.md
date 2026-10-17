# ADR-1418 · Saved deadline alert investigation from the CLI

Date: 2026-10-16
Status: Accepted

## Context

Go clients can map a saved deadline alert to validated current triage. Operators
using the CLI otherwise need custom Go code or manually reconstructed filters.

## Decision

Expose the helper as `events deadline-alert-investigate APP --alert-file PATH`,
with bounded page size, opaque cursor continuation and global JSON output. Accept
one Go alert object in a regular bounded local file; reject ambiguous/duplicate,
unknown, null or case-aliased fields and multi-object/envelope input. Reuse the
protected file opener and keep the file-size limit in the central API limits file.

Reuse the Go helper's selection, scope/response validation, timeout and authorization.
Show notification, traversal and current observation clocks separately. Print full
quoted identities/cursors, current waiting/scheduling metadata and a current-membership
label. Keep live pagination and ratio-numerator-only semantics explicit.

Perform no automatic page walk, alert acknowledgment, checkpoint update, notification,
replay or delivery mutation. Add command dispatch/help metadata and documentation
without a new backend route or schema migration.

## Consequences

Operators can inspect current delivery identities from saved alerts without custom
code. A bounded page is not a historical cohort or successful-delivery guarantee.
Saved Go/CLI alerts remain distinct from Node/Python checkpoint alert formats. Cursor
expiry and errors propagate rather than silently starting another traversal.

See [CLI usage](../event-deadline-alert-investigation.md#cli-investigation).
