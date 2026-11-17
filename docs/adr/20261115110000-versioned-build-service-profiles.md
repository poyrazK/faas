# ADR-20261115110000 · Versioned build-service coverage profiles

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [running-build evidence](20261115080000-advisory-runtime-build-evidence.md),
  [collection](20261115090001-monitoring-build-evidence-transport.md) and
  [observed history](20261115100001-observed-build-history.md).

The original build profiles share the minimal input-health definitions and omit
the builder and image services, although both already export build identities
and have static/scrape discovery definitions. Freeze the original build coverage
as independent build-profile definitions. Preserve `single-box-v1` (API, public
gateway, scheduler and VM manager) and `control-plane-v1` (API, public gateway,
control-plane scheduler, compute gateway/scheduler and VM manager), including
job order, selectors, inventory expectations and advisory behavior.

Add explicit `single-box-v2` and `control-plane-v2`. Append `builderd` and
`imaged` to the corresponding v1 build jobs. Single-box uses static scrape
membership with minimum one target per service. Control-plane compares exact
node name and node ID membership for both services against the active configured
compute registry, matching their existing discovery scope. An unavailable
registry remains unknown; a successfully observed empty registry is legitimate.
Inactive/unconfigured nodes remain outside this scope. Operators select v2 only
when these services are part of the intended placement; do not infer a profile
from observed targets or silently upgrade v1.

Use a build-specific job spec with the complete build metric family, independent
of readiness/input-health role definitions. Telemetry input-health continues to
accept only its two v1 roles. Build-profile versioning does not expand or alter
those readiness/input checks. Preserve the existing no-role diagnostic and
default verifier behavior.

Declare `ObsRuntimeBuildJobsMax=8` in `pkg/api/limits.go` and extend the operator
API schema's build-job array bound accordingly. Up to eight fixed readers share
the existing three-second deadline and one pinned evaluation time. Retain the
4096-sample/vector and 1000-node bounds, 120-second freshness and 30-second skew,
32-item preview, 64 KiB API response, 16-event/16 KiB history and 1 MiB report
envelopes. Aggregate all inspected jobs before previewing. No new probe, metric
producer, scrape configuration, deployment action or health gate is introduced.

Carry all four profile choices through API selection/closed validation, CLI
`--expected-role`, collection `MONITORING_EXPECTED_ROLE`, current evidence,
history, artifact projection and summaries. Validate each exact versioned job
set; neither a partial v1 list labelled v2 nor an expanded v2 list labelled v1
can establish identity evidence. Regenerate preview SDK enums/operation types
and preserve their older profile values. An older API rejecting v2 remains
advisory unknown; do not retry as v1. An older CLI unable to accept the profile
retains the existing unknown-verification behavior.

Test all profile definitions, v1/input-role compatibility, new-service missing,
stale, conflicting and mismatched observations, exact registry membership beyond
the preview, unavailable/empty inventory, read authorization transport, unchanged
health qualification, v2 history/summary retention, downgrade rejection, bounded
responses and generated-client round trips. Operational profile activation stays
with the deployment owner.
