# ADR-823: Per-route request labeling consistency

Status: Accepted

## Context

The overall labeled CPU share can stay constant while labeling changes for a
critical route. Sample labels alone cannot count requests or establish request
instrumentation coverage.

## Decision

The Go collector maintains a bounded per-capture census of labeled request entries.
ServeMux and RouteHandler count request entries automatically. WithRouteRequest
supports other routers explicitly; WithRoute remains execution-only and does not
invent request counts. Nested wrappers sharing a request context count once;
applications should instrument the final matched route once.

Counters run only during an active CPU capture and are reset with each capture.
Discard reports with old-epoch CPU buffers, polling failures or cancelled
collection. Snapshot/resume retains the existing epoch admission checks.
Caps are 50 routes and one billion entries per route/capture; caps make the report
incomplete. The wire header is bounded at 16 KiB decoded. Optional invalid counters
are discarded while otherwise valid CPU uploads continue.

The host admits only labels in its current explicit route allowlist, rejects
report windows outside the VM lifetime or capture window, and allows one second
of pprof writer timestamp tolerance. Convert allowed labels to stable 128-bit
SHA-256 fingerprints in a bounded compact report inside the existing coverage
record. Rejected labels are not stored. Metadata remains within existing per-frame
symbol limits and adds no samples or backend series. Readers support legacy
coverage, attribution counters and the new optional request counter extension.

Aggregate counts only from reports wholly within the selected query window.
Exclude boundary reports without apportioning counts. Missing, malformed,
allowlist-rejected or incomplete reports make labeling evidence unavailable.
Project internal fingerprints onto requested route summaries; fingerprints are
not exposed in public DTOs or saved summaries.

Compare reported labeled entries from retained captures with observed weighted
route traffic in the whole deployment window. Counts exceeding observed traffic
are unreconciled, never clamped to 100 percent. Advisory route CPU/request requires
at least 80 percent observed labeling share on both sides and a change of less
than 20 percentage points. Missing evidence or failed thresholds yields
insufficient_data without altering aggregate results, retries or rollout state.
Retain route labeling summaries with existing assessment and canary history.

## Consequences

These are application-reported counts, not authoritative request attestations.
Capture gaps, boundary exclusions, request-entry versus telemetry timing and
minute-bucket boundaries can depress or distort the ratio. High shares cannot
prove every request was labeled consistently. The guard is conservative and
advisory. Legacy collectors and Node/Python lack these reports and cannot satisfy
the new route consistency check. WithRoute users must adopt WithRouteRequest for
request counting.

Upgrade apid before profiled, then deploy updated guest-init images and Go
applications. Old guest-init ignores the header; old API coverage readers cannot
parse the extension. Live merge, runtime and browser acceptance remain pending.
