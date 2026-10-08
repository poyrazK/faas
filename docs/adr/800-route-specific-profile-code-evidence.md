# ADR-800: Route-specific profiling code evidence

Status: Accepted

## Context

ADR-799 detects route CPU/request regressions against a pinned baseline.
Application-wide hotspots cannot identify which code consumes a selected route's
CPU; unrelated traffic can dominate those hotspots.

## Decision

After qualifying the existing aggregate capture, route attribution and request
label checks, query both exact windows with the configured route filter. Reuse
the bounded regression comparison with CPU/request normalization and the route's
observed request counts. Store ranked threshold-crossing function and complete
call-path evidence on that route check. One-sided and zero-baseline symbols
remain unknown. Missing code evidence does not alter the route check or rollout.

Extra queries share the existing parent deadline and admission slots. Route
summaries retain no raw samples or source URLs and fit the existing assessment
budget (8 KiB of code evidence per route); truncate lowest-ranked trailing evidence when necessary. Commit-pinned
source links are derived from owned deployment provenance at read time. Incident
comparison URLs preserve the route, frozen windows and matching call-path
metadata. Saved investigations continue retaining their aggregate selections;
the route evidence is a bounded sub-comparison, not aggregate attribution.

## Consequences

Route investigations can connect advisory resource regressions with sampled
functions and paths. Inclusive paths overlap and evidence establishes association,
not code-change causality. Retention expiry can prevent restoring highlighted
frames while the bounded historical summary remains available. Multiple routes
share a deadline, so later route code evidence can be unavailable under load.
