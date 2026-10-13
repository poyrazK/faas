# ADR-1019: Recovery health alert incidents

## Decision

Add optional `health_alerts` to the full recovery policy replacement, with explicit `enabled`, `stall_after_seconds` and `alert_after_seconds`. Both durations are 1–86400 seconds. Existing policies remain opted out. These persisted monitor thresholds are independent of the read-only health request's threshold. Policy updates retain version CAS and existing write/MFA authority.

Use the shared recovery health calculation in a bounded scheduler monitor. Reuse the existing per-minute poll and batch limits. Store one latest incident per app, independent of policy version, and schedule its next observation on the policy row. PostgreSQL claims and locks the policy first, obtains a repeatable-read queue snapshot, and commits incident state and notification outbox atomically. Memory uses its existing mutex. Control, execution and replacement writers use the same policy fence. Deleted apps are excluded.

An unhealthy observation (`paused`, `blocked` or `stalled`) opens an incident. Capture one `automation.recovery_health_alert` after the configured duration. Reason and severity changes update that incident without repeated notifications. An incomplete/unknown health snapshot cannot open or resolve an incident and postpones initial alert capture. Healthy or inactive health closes the incident; capture one `automation.recovery_health_recovered` only if the initial alert was captured. Inactive resolution reports disabled execution explicitly and does not claim a successful child. Turning off health alerts closes silently; re-enabling after closure creates a new incident if needed.

Freeze alert receivers at capture. Record `no_receivers` even when no event is enqueued, and never replay to later subscribers. Recovery receiver IDs are the intersection of original alert receivers and currently enabled, app-scoped receivers subscribed to recovery. Use deterministic event IDs from incident ID and transition type and the existing signed webhook outbox. Historical payloads contain only app identity, configuration version, health reason, queue counts, observation timestamps and the health URL. Subscribe to both event types or an empty event filter to receive paired notifications.

Expose the latest incident and capture receipts in the consistent queue snapshot, including the health response. Reads do not advance monitoring, refresh evaluation timestamps or enqueue events. Keep immutable historical payloads in the existing delivery ledger, rather than growing a separate incident-history table. The append-only migration guards rollback when configuration, incidents or notification evidence exist.

## Consequences

The monitor observes sampled health; it does not guarantee scheduler liveness or send while the entire scheduler is down. Alert and recovery deliveries can arrive after the condition changes or out of order; receivers deduplicate on event ID and correlate on incident ID and timestamps. Previously captured deliveries remain historical intent after configuration changes. A platform-external heartbeat is needed to detect complete scheduler outage.

No health monitor operation retries or cancels a workflow, suppresses an action, changes policy authority, refunds charges, finalizes a child or releases its execution slot. Existing action-specific blocked and failure-pause notifications remain separate events.
