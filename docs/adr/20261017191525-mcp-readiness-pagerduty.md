# ADR-20261017191525: Native PagerDuty readiness delivery

## Context

Readiness incidents have durable independent primary/fallback queues, but native
PagerDuty integration still requires an external webhook adapter. Integrations must
preserve incident identity, credentials, recovery ordering and uncertain outcomes.

## Decision

Offer versioned PagerDuty configuration for the primary receiver of an existing
application readiness webhook rule. Use the fixed US Events API v2 endpoint and a
dedicated age namespace for write-only integration keys. Preserve webhook fallback.
Pin the provider, encrypted key and private key fingerprint at first delivery.
Configuration changes apply to unattempted incidents. Hash-based private receiver
identity distinguishes integrations behind the same API URL; webhook URL ordering
remains unchanged. Legacy attempted incidents remain on their existing transport.

Use one stable incident dedup key for trigger, reminder, retry and resolve. Only a
validated 202 acknowledgement with matching key is accepted. Never equate acceptance
with paging. Keep acknowledgement independent; this is outgoing delivery only.
Never follow redirects, expose credentials or retain raw response/error contents.

Complete native outcomes, physical delivery history and provider retry deadlines
atomically within the existing app → rule → event locks and fenced lease. Respect
bounded Retry-After on 429/503 and the existing rule cooldown. Unknown outcomes
conservatively retain primary recovery, including after fallback. Preserve existing
maintenance, acknowledgement and reminder freshness guards.

Expose masked API, dashboard, CLI and Go/Node/Python contracts. Upgrade every
readiness evaluator before enabling; rollback protects configuration and routing
history. Validate against a local fake provider and qualify real services separately.

## Consequences

Customers can page directly without writing a webhook adapter. Existing rule webhook
configuration remains available when native primary is disabled. Existing integrations
must remain valid until pinned incident recoveries finish. Regional endpoints, native
PagerDuty fallback receivers and inbound acknowledgement synchronization remain
separate work. Provider acceptance has explicit inspection semantics.
