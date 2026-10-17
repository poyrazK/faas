# ADR-1396 · Sustained preview-based consumer delivery alerts

Date: 2026-10-15
Status: Accepted

## Context

Operators can preview consumer pacing and modeled deadline risk, but need ongoing
monitoring with explicit freshness, coverage and recovery confirmation. An isolated
preview or bounded/truncated count must not imply sustained delivery health.

## Decision

Add opt-in Go SDK delivery-health watch conditions for growth between complete
previews while the shared token bucket is throttled, and aggregate modeled deadline
risk, plus a configurable approaching-deadline window. Extend the existing preview
response with optional effective-deadline coverage, count and earliest timestamp.
Missing metadata on older servers leaves the approaching-deadline condition unknown
without disabling the other signals. Encode the window in checkpoint metric identity.
Read current settings and a matching read-only preview; validate scope,
revision, capacity, clocks and aggregate coverage. Preserve historical timestamp
high-water marks to reject replay while rebuilding live growth baselines after
resume, gaps, configuration changes or evidence loss.

Reuse the existing condition/outbox/checkpoint machinery. Require two eligible
observations over separate warning and recovery sustain durations. Preserve firing
incidents during pending recovery; incomplete or missing evidence causes unknown
or confirmation_lost, never false resolution. Checkpoints use version 2's existing
sustained-recovery fields and validate the new count metrics and bounded immutable
contributor summaries. Pending-count and sustain proof are not restored as live.

Expose the preview and growth diagnostics in progress. Alert summaries identify
observed rate/pause/circuit/capacity/lease/scheduling contributors without copying
payloads or risk identities into durable alert history. Require full pending sample
coverage; permit independently truncated identity details when aggregate counts are
complete. Keep rate-only predictions advisory and preserve existing lifecycle,
ordering, retention, retry, deduplication and acknowledgement behavior.

## Consequences

Enabled watches make two additional read-only requests per health poll. Existing
watches remain unchanged. Node/Python watch options are outside this Go SDK slice;
no new endpoint, platform migration or automatic message delivery is introduced.
Aggregate growth is per observation interval and cannot be inferred from a capped
pending sample. Model-risk alerts do not certify that all other deadlines are safe.

See [configuration and evidence semantics](../event-delivery-rate-risk-alerts.md).
