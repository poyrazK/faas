# ADR-20261113110000 · Receiver evidence in operator notification health

- **Status:** accepted for implementation; production rollout pending.
- **Date:** 2026-11-13
- **Extends:** [aggregate notification health](20261016130000000-operator-notification-health.md) and [receiver transport evidence](20261113090000-notification-receiver-evidence.md).

Add optional `receiver_health` to the existing operator notification-health
response and CLI. Preserve the top-level aggregate state, signals, integration
counters and limitations. The new section has an independent assessment;
older clients ignore it and the new CLI explicitly reports older servers'
missing receiver section as unknown. No new endpoint, permission or write is
introduced. Operator scope, MFA and allowlist enforcement remain unchanged.

Read deployment intent, collector health and run clock, completed request
counters and success observations from Prometheus. Four bounded concurrent
queries use strict vectors, one pinned evaluation time and the existing shared
three-second request budget. Scrape timestamps establish freshness; the
collector's own run clock prevents fresh scrapes of frozen files from passing.
Private scrape identity binds each metric and timestamp to the same source.
Duplicates, invalid labels, missing counterparts and stale or future clocks
cannot establish health. Retired groups appearing only in historical counter
ranges cannot become current receiver groups.

Expose at most 32 reviewed receiver/integration groups, matching the existing
notification observation bound. Only the three fixed receiver roles and
reviewed transport types are returned. Raw target labels, addresses, provider
responses and credentials are excluded. Query failures remain generic reasons.
Independent valid failures take precedence over missing peer evidence.

Fresh zero counter increases remain distinct from missing or single-sample
counters. Unknown deployment intent produces `expected: null`. A cold success
baseline produces a null last-success value; it never creates a 1970 receipt.
The success value is the collector's confirmation clock, while its signal's
observation time establishes gauge freshness. Earlier confirmed history may
remain valid after a restart. Multiple destinations sharing a group aggregate;
per-alert delivery, human receipt and external-watchdog receipt remain unproven.

Tests cover source identity, freshness, duplicates, unknown metadata, partial
reads, bounds and shared cancellation. Real Prometheus fixtures cover query
labels, source timestamps, single samples, resets and retired ranges. CLI and
generated SDK checks preserve compatibility, nulls and closed receiver types.
See the [operator procedure](../ops/notification-delivery.md#operator-api-and-cli).
