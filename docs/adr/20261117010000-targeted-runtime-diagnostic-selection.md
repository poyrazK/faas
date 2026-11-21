# Targeted runtime diagnostic selection

Date: 2026-11-17

## Context

The protected runtime-build read collects multiple advisory groups within a
shared three-second budget and 64 KiB response. Full-set summaries survive
clipping, but useful target details can be lost. Focused investigations need
fewer source reads and more room for the requested evidence. An intentional
omission must remain distinct from missing or stale evidence.

## Decision

Add optional `diagnostics` selection to the existing protected API and
`--runtime-diagnostics` to the deployment verifier. Omitted options retain the
existing behavior and wire shape. `all` selects supported advisory groups;
`none` requests only core evidence; a distinct comma-separated list selects
individual groups. Reject empty, duplicate, unknown and unsupported names before
source reads. Require an explicit expected role.

Build identity, readiness, process evidence and loop health are always collected.
They are not selector options and their scope, budgets and qualification rules
are unchanged. V1 contains only core jobs, so both `all` and `none` keep its core-only scope.
Named advisory groups require V2: builder_health, handoff_health, preparation_health,
build_recovery_health, app_task_health, command_cron_health,
command_cron_outcomes, http_cron_delivery, command_cron_timing, app_task_results,
app_task_operations, post_capture_health and snapshot_prime_health.

Explicit API requests return a `selected_runtime_diagnostics` descriptor with
canonical `selected` and `not_selected` lists partitioning the role's supported
groups. Selected data can still be unavailable. Unrequested groups perform no
source reads and are omitted. The existing deadline, response cap and preview
limits still apply.

The CLI validates the acknowledgement against its requested intent, filters
unexpected extra advisory groups locally, and retains core evidence independently
of invalid optional metadata. A 400 response to an explicit selector permits one
retry without that query key. Cache unsupported selection for the verifier run.
Reports label acknowledged selection as `server` and other filtering as `client`;
client filtering does not establish reduced source query load. Normal polls keep
the existing protected-read count; the compatibility retry adds at most one
runtime-build read when unsupported selection is first encountered.

Carry selection through SDKs, report projection and inline history metadata.
The SSH collector accepts `MONITORING_RUNTIME_DIAGNOSTICS`, validates it before
SSH and passes canonical choices. A default collection cannot be narrowed by an
unsolicited descriptor in the returned report.

Intentionally unselected groups do not create missing-evidence progress. Preserve
past observations, but interrupt continuous claims and sampled state transitions
on deselection. A first selected observation after prior intentional omissions
is not itself a source gap. Counter differences retain their existing independent
process and sample-window guards; overlapping poll differences are never summed.

## Consequences

Focused views retain more useful details and reduce advisory source requests
without weakening core evidence or deployment gates. Missing selected data is
still visible as unknown/incomplete. Defaults remain compatible; opt-in older-API
collection remains useful through explicitly labelled client filtering.
