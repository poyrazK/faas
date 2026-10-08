# ADR-793: Route-associated CPU profiling

Status: Accepted

## Context

Aggregate CPU and request-mix snapshots cannot identify which routes consume
CPU. Go pprof supports execution labels, but backend stack merging may discard
those labels. ADR-792 discards arbitrary guest sample labels.

## Decision

Admit only `gregale_route` labels equal to a host-authorized explicit declared
method/path pattern. vmmd resolves the environment route policy, falling back to
app declarations only when no environment policy exists. Other policy lookup
failures admit no labels. Bound labels to 256 bytes and 50 sorted unique patterns.
All other sample labels and comments remain discarded. This is a narrow exception
to ADR-792; host-owned account/deployment/scope isolation remains authoritative.

The initial runtime adapter is Go's scoped pprof labels. A ServeMux middleware
looks up the matched static pattern and labels normal mux dispatch; it preserves
wildcard values and uses the actual request method. Explicit wrappers support
other routers. Modern Go routing is required; legacy mux mode disables automatic
attribution. Child goroutines inherit
labels; an explicit helper clears them for background execution. Node, Python,
legacy captures and unrecognized labels remain explicitly unattributed.

The Pyroscope adapter replaces admitted labels with reserved synthetic root
frames before storage. Guest-supplied reserved markers are sanitized first.
Public profile views extract attribution and remove markers before rendering
functions and flamegraphs. Encoding limits preserve CPU as unattributed rather
than dropping samples. No route or guest tenant selectors are forwarded to the
backend.

API, CLI, dashboard and saved investigations accept matching route filters for
both comparison windows. Optional deployment-scoped request telemetry provides
route CPU/request. A common-mix comparison requires sufficient capture coverage,
matching routes, no unattributed CPU, complete observed request-route coverage,
and at least 20 requests per route/window under default regression options.
Weights average baseline and candidate request shares. The result is explanatory
context and does not alter canary gates.

## Consequences

Route-associated sampled CPU is not exact per-request accounting. Background
work with inherited labels can distort attribution, and matching observed route
sets does not prove full instrumentation. Collection coverage remains capture
coverage. Explicit declarations use current host policy at ingestion; inferred
OpenAPI routes need future host authorization support. Live Pyroscope merge,
native runtime and browser acceptance remain operational validation work.
