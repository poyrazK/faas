# ADR-829: Gregale route sunset report

Status: Accepted

## Context

ADR-828 publishes lifecycle dates to clients. Owners need a migration queue
that combines those dates with retained callers and reviewed successors.

## Decision

Add `gregale routes sunsets` as a read-only CLI composition of existing APIs.
Read current manual-import metadata, selected deployment route-customer usage,
and, optionally, fresh explicit route-pair contract reviews and successor usage.
Never infer operation mappings from successor URLs. Pin contract reviews to
explicit deployments and require all telemetry windows and response identities
to match the requested app/deployment/window.

Report metadata errors and missing fields instead of hiding operations. Rank
elapsed and upcoming sunsets first. Preserve caller truncation, anonymous and
unresolved identities, retention clamping, and observed-only coverage. Compare
caller identity pairs only within an app. Report hashes for imported metadata
and reviewed captures; the two contracts have different ownership and time
semantics. Reports and CI exits remain advisory, with no removal authority.

## Consequences

No server endpoint, schema migration, or automatic removal is introduced.
Observed successor usage is not proof of migration order or complete adoption.
The report is bounded by existing migration and analytics limits and can be
saved without overwriting an existing file.
