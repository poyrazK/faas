# ADR-1558: Native PagerDuty readiness fallback

Status: Accepted

## Context

Readiness fallback currently requires an external signed webhook adapter to page
on-call engineers. Native primary PagerDuty delivery, regional endpoints, durable
receiver queues and signed acknowledgements already exist. Fallback must retain
the same incident identity and must not lose recovery when receivers change.

## Decision

Extend the existing versioned fallback policy with an optional provider selector.
Omitted provider remains webhook. Webhook configuration keeps its URL and signing
secret. PagerDuty requires a write-only Events API key, optional US/EU region
(default US), and the existing 1–10 completed-primary-failure threshold. Provider
fields are mutually exclusive. Disabled policies omit destination fields. Persist
provider, region, sealed integration key and private fingerprint with checked
columns; old policies remain webhook. Reject identical primary/fallback PagerDuty
region and integration fingerprint in either setter under app → rule locks.

Pin the fallback provider, regional endpoint, integration fingerprint, encrypted
key and policy version on first dispatch, alongside the existing primary route.
Use the existing independent receiver FIFO queues and sticky activation rules.
Trigger, retry, reminder and resolve share the incident dedup key. Provider
acceptance means accepted by PagerDuty, not paged. Preserve retry deadlines and
fenced completion. Refresh encrypted credentials only for an identical pinned
provider/receiver identity. Different integrations or regions and disabling affect
unattempted incidents; keep old integration keys valid until recovery completes.
Recovery still reaches fallback and every informed or uncertain primary.

Extend the current revocable acknowledgement binding with destination (default
primary). Primary uses expected_pagerduty_version. Fallback uses
expected_fallback_version instead, capturing an enabled native fallback's region
and fingerprint. There is one current binding per rule, selecting one destination.
Require activated fallback and the pinned fallback integration when matching its
signed callbacks. Retain destination in new minimal receipts; legacy receipts with
no destination mean primary. Existing tenant/service checks, HMAC verification,
policy fencing, duplicate suppression, stale-event handling and secret revocation
remain in effect. Provider events only acknowledge attention; probes determine
health and recovery. Disabling outgoing fallback does not revoke an existing
incoming binding for old pinned incidents; disable acknowledgement sync explicitly.

Expose both fallback providers and the selected acknowledgement destination in
API, dashboard, CLI and Go/Node/Python SDKs. Administrative writes retain admin,
MFA and browser CSRF guards. Mask credentials and never return fingerprints.
Stop readiness evaluators for migration, upgrade every evaluator and apid, then
resume before enabling. Append-only migration
rollback refuses native fallback configuration, routing or acknowledgement history.

## Consequences

A failing webhook can escalate directly to PagerDuty without another service.
Existing webhook clients retain their default behavior. A rule can select separate
native primary and fallback integrations, but this does not provide independence
from a shared PagerDuty outage. Multiple fallback tiers, simultaneous inbound
bindings for both destinations and automatic provider acknowledgement release
remain separate work. Local provider/signature contracts do not substitute for
live subscription and service/key qualification.
