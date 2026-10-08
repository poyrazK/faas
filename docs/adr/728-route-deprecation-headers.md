# ADR-728: Operation deprecation and sunset headers

Status: Accepted

## Context

Route removal approval protects production cutovers, but clients need migration
information before an operation disappears. Imported OpenAPI documents already
provide an app-owned operation metadata store and notification-invalidated
routing cache.

## Decision

Use `deprecated: true` with `x-gregale-deprecated-at` and optional
`x-gregale-sunset-at` and `x-gregale-successor` on imported operations. Dates
are whole-second RFC3339 values after the Unix epoch; sunset cannot precede
deprecation and requires an absolute HTTPS successor URL. Reject invalid
metadata at the import boundary and when compiling a cached document.

The gateway publishes RFC 9745 `Deprecation`, RFC 8594 `Sunset`, and a
`Link` with RFC 5829 `rel="successor-version"`. Match method and original
public path using existing declaration specificity and implicit HEAD semantics.
Lifecycle annotations come from the imported document even if an explicit
allowlist governs admission. Lifecycle lookup failures omit advisory headers;
existing allowlist failures still reject requests under their existing policy.

Pinned deployment URLs omit app-wide annotations because they must not inherit
a mutable application contract. Deployment-specific lifecycle publication is a
future extension. Headers are attached after CORS and route admission, before
authentication and forwarding; earlier edge answers do not receive them.
Upstream headers retain the existing proxy header behavior.

## Consequences

Metadata is stored and served in the existing raw imported OpenAPI document;
no database migration or separate lifecycle authority is introduced. Sunset is
advisory and never automatically disables traffic or bypasses removal approval.
The successor link communicates a migration destination, not compatibility
proof. Upcoming-sunset inventory and caller reports remain a subsequent CLI
increment using the existing migration evidence.
