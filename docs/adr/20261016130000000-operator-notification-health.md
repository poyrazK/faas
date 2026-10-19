# Operator notification health and suppression evidence

Status: Accepted, 2026-10-16. Extends retained platform alert evidence.

## Decision

Expose GET `/v1/admin/obs/notification-health` and
`gregalectl obs notification-health --json`. The read uses existing Prometheus
scrapes and a new bounded, read-only Alertmanager alert-status reader. Operator
scope, allowlist and MFA gates match incident evidence. Query parameters are
rejected. Reads do not write audit events, collection history or triage.

Report target availability, last configuration reload success, current
Prometheus notification queue length/capacity, ten-minute dropped notification
increase, and attempts/failures by integration type. The last two counters
include all receivers and watchdog traffic; receiver-specific delivery is not
observable from these aggregates. Counter increases are extrapolated floats.
Integration names come from a reviewed closed vocabulary; receiver names,
addresses and raw labels never appear. New integration types make that source
unavailable until reviewed, rather than being silently dropped.
Alertmanager 0.27 preinitializes counters for every supported integration type
when receiver-name metrics are disabled. Counter presence therefore does not
establish configuration or readiness of that integration. The response exposes
this limitation and does not read receiver configuration to infer readiness.

Each signal has independent evidence state, observation time and nullable value.
Freshness uses the oldest last-observed source sample over a bounded 15-minute
lookback. Stale, absent, invalid, warning-bearing or failed observations are not
zeros. Strict vector reads reject malformed samples. Quiet initialized counters
can supply known zero values; counters with insufficient samples stay unknown.
No cache reuses another request's values. Missing counterparts in the integration
union also make overall evidence partial. A source failure cannot hide a valid
positive failure count from another source or integration.

Overall degraded takes precedence when a fresh observation shows notification
failures or drops, a down target, failed config reload, zero queue capacity, or
queue pressure above 80 percent. Otherwise missing evidence gives partial;
healthy requires every signal, every observed integration counter and the
bounded alert-status API read to succeed. This is a point-in-time observation,
not the sustained condition used by the existing Prometheus paging rules.
Alert API reachability does not establish configured receivers or delivery.

All remote observations share a three-second context deadline. Prometheus
and Alertmanager reads proceed independently so a slow alert-status API does
not consume the metrics deadline before those reads start. Counter sums require
increase samples for every currently selected series; a new single-sample
series cannot silently disappear from an otherwise usable aggregate. Prometheus
responses retain the existing byte/series bounds. Alertmanager replies are
bounded at 1 MiB and 4096 alerts, decoded as one complete JSON array. Partial,
null, oversized or malformed snapshots cannot establish unsuppressed state.
The client reads only `/api/v2/alerts`, including active, silenced, inhibited and
unprocessed alerts. It never reads configuration, silence comments, secret
files, addresses or receiver credentials; errors are closed generic reasons.
Production transport refuses redirects and allows only literal loopback HTTP(S)
destinations. It does not resolve hostnames or use a proxy.

`FAAS_ALERTMANAGER_URL` defaults to `http://127.0.0.1:9094` on control-plane
roles and is unconfigured elsewhere. Explicit `disabled` disables the reader.
Invalid configuration is reported as unavailable without issuing a request.
This changes no notification routing, deployment inventory, service units,
receiver configuration or rollout behavior.

## Incident suppression identity

Platform-alert incident evidence carries an optional `suppression` object.
Resolved episodes return not_applicable/historical_episode; live fallback
without an immutable episode remains unknown. For a current firing episode,
re-read fresh complete Prometheus rules, reconstruct immutable episode IDs, and
require the exact episode, series and activation. More than one current rule
with the same series is ambiguous and cannot establish a join.

Join Alertmanager labels using the same full series fingerprint, adding only
Prometheus's synthetic `alertstate=firing`. Do not infer joins by alert name,
component or account, or remove arbitrary external labels. Changed labels,
external labels, absent or duplicate matches remain unmatched. Alertmanager's
startsAt may follow activation because Prometheus waits through the rule's for
duration. Alertmanager 0.27's strfmt.DateTime JSON truncates to milliseconds;
compare the activation lower bound at that same wire precision. An earlier
millisecond remains a mismatch; do not add a broader activation tolerance or
alter the exact Prometheus episode/series verification. UpdatedAt must follow
startsAt, be within the freshness/skew bounds, and endsAt must be in the future.
Stale, inconsistent, clock-invalid or unprocessed evidence cannot supply false
unsuppressed booleans. Suppressed/active supply current silenced and inhibited
booleans without exposing silence IDs, actor identities or comments.

This read does not reconstruct historical silences or prove per-episode
notification delivery, human receipt or external watchdog receipt. Operator
triage snooze continues to affect workflow only; it does not mute Alertmanager.
Incident status, suppression and notification health remain independent.

CLI human output preserves unknowns; JSON and diagnostic bundles retain only
closed suppression fields. API source failures yield partial HTTP 200 results,
with unavailable suppression included in incident unavailable_sources. Frontend
and rollout remain separate work. This implementation is committed locally.

## Validation

Test known-zero versus absent counters, individual failures, stale and future
source clocks, query warnings and malformed samples, unknown integration names,
counterpart gaps, shared cancellation, failed APIs, bounded decoding and refused
redirects/configuration destinations. Test exact suppression, recurrence and
rule-change isolation, ambiguous rules, external/removed labels, duplicate
Alertmanager matches, stale/expired/unprocessed and inconsistent states, and
historical evidence. Verify API scope, allowlist and MFA, CLI validation and
error redaction, embedded OpenAPI, Node generation, environment contract and lint.
