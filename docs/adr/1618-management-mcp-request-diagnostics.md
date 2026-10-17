# ADR-1618: Exact retained request diagnostics through management MCP

## Status

Accepted. Extends ADR-1250, ADR-1251 and ADR-1259.

## Decision

Expose read-only `gregale_get_request` and `gregale_get_request_evidence` using
the existing app-scoped native debugger client methods. Accept exact public
request IDs, telemetry row IDs and trace IDs without treating opaque public IDs
as UUIDs. Reject standalone dot path segments to prevent HTTP path normalization.
Preserve the lookup separately from native returned identities because
the request-ID journal resolves public aliases to retained telemetry. Native
ownership, plan, credential and retention enforcement remain authoritative.

Add optional per-app OAuth `request_read_apps` review, unchecked by default.
The permission is explicitly app-wide across environments, independent of
default/named environment consent, log reads and writes. Persist slug/immutable
app-ID pairs within the existing encrypted consent record; absent fields deny
access. Approval validates against both the reviewed app inventory and chosen
app access before publishing the grant. Refresh/restart cannot widen selection,
and account-wide grants do not include future diagnostic apps. Local stdio
continues using native credential authority. No migrations are needed.

Pin app identity before requesting native diagnostics and check it again after
the generic tool wrapper's final authorization, immediately before serialization.
Verify any attributed deployment's ID, app ID and stable scope as well. Diagnostic
consent permits that attribution across environments without granting generic
deployment inspection in those environments. Journal-only evidence retains
unavailable deployment/scope rather than inventing default attribution.

Project explicit fields only: request status/route/method/timing, bounded timeline,
correlation stages and aggregate dependency timings. Centralize transport bounds
in `pkg/api/limits.go`: request identifier 128 UTF-8 bytes, string fields 256 bytes,
timeline 32 rows, correlation 16 rows and dependencies 16 rows. Redact known
credentials before truncating strings. Preserve source and projection truncation,
approximation and missing-stage evidence. Never claim complete correlation from
empty, missing or truncated stages/spans. Exclude query/fragment data from routes,
raw spans/attributes, SQL, bodies, headers, actors, host/consumer identities and
native explanation/regression payloads. Durations can overlap and are not a
critical-path sum. Reads remain sequential observations, not an atomic snapshot.

Provide `gregale_request_diagnosis`, a read-only exact-selector recipe with the
same consent guard. App completion checks consent and current native app identity;
request IDs are never listed. Context, preflight and owner summaries expose
consent separately from native endpoint availability or entitlement. Existing
connection revocation cancels active calls and blocks subsequent discovery/reads.

## Consequences

Agents can investigate a known request without exporting raw traces or widening
ordinary environment access. Application text can still be private and must be
treated as untrusted data. Native 403/404 and other errors remain visible; missing
evidence does not prove health. No replay, model call, probe, collection or platform
mutation is added. Portable fixture and real Codex acceptance verify bridge
consent/redaction/restart/refresh/revocation; they do not qualify provider or native
deployment behavior. Native API and database contracts are unchanged.
