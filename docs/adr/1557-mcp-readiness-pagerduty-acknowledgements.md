# ADR-1557: Signed PagerDuty readiness acknowledgements

Status: Accepted

## Context

Native regional delivery preserves incident identity, but acknowledging a PagerDuty
page does not stop Gregale reminders. Incoming provider data must not acknowledge a
foreign incident, undo a newer local decision, or change health and recovery.

## Decision

Offer a versioned, revocable per-rule acknowledgement webhook policy. Enabling
requires the current enabled outgoing PagerDuty policy version, a service ID and
the v3 subscription signing secret. Capture the outgoing integration's private
fingerprint and region. Return masked settings and a stable relative webhook path,
including before configuration. Store the secret in a dedicated age namespace.
Only the current policy secret is accepted. Replacing it with a fresh secret or
disabling settings immediately revokes the old signing credential. Resubmitting
the same secret does not rotate it. Old secrets and configuration snapshots are
not retained.

Verify the exact raw-body HMAC-SHA256 using the official comma-separated v1
signature format. Bound body size, signature count, JSON depth and identifier
lengths. Reject ambiguous duplicate JSON keys. Accept incident.acknowledged only
with a Gregale incident dedup key, matching service, regional API reference and
pinned incident integration. Ignore unrelated services/incidents and unsupported
event types. Do not fetch provider URLs. Reject events older than seven days or
more than five minutes in the future.

Under app → rule locks, recheck the enabled policy version after authentication.
Atomically persist a minimal provider event receipt, any attention action and
queued reminder suppression. Replay the original receipt for an identical event;
reject conflicting reuse of its ID. Retain at most 128 provider receipts per
incident; further new IDs are acknowledged without mutation. History pages are
bounded to 50 entries. Receipts retain service, region, provider IDs and times,
policy and attention versions, and outcome; never raw payloads or actor details.

Preserve acknowledgements and their expiry. Events no newer than the latest local
attention decision are stale. Existing acknowledged incidents are not extended;
resolved incidents remain resolved. A newer acknowledgement after a local release
may acknowledge again. Readiness probes remain authoritative for health and
recovery. Unacknowledged/resolved provider events do not release acknowledgements
or change health. Manual release remains available.

Expose configuration and event history through API, CLI, dashboard and Go/Node/
Python SDKs. Provider ingress uses HMAC authentication and the shared failed-auth
IP limiter, with no Bearer or browser-session requirement. Administrative writes
require admin scope, MFA and browser named CSRF where applicable; reads require
read scope and MFA. Immutable attention history identifies the provider source.

## Consequences

A PagerDuty acknowledgement stops Gregale reminders without a second manual action.
Outgoing configuration changes never retarget the acknowledgement binding. A new
integration or region needs an explicit webhook binding update; already pinned
incidents of the old integration stop accepting callbacks after such a rebind.
Disabling outgoing delivery alone preserves acknowledgement sync for matching old
incidents. Disable webhook sync explicitly to revoke it.

Apply the append-only migration and upgrade apid/evaluators before enabling to
preserve provenance on attention snapshots. Rollback refuses configuration or
provider audit history. Provider alert grouping must preserve Gregale's incident
key; grouped/manual incidents without that key are ignored. Local signature and
lifecycle contracts are independent of live PagerDuty qualification. Native
PagerDuty fallback and bidirectional acknowledgement release remain separate work.

References:
- https://developer.pagerduty.com/developer/verifying-webhook-signatures
- https://developer.pagerduty.com/docs/webhooks/v3-overview
