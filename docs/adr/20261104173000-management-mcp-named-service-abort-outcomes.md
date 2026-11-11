# ADR-20261104173000: Named-environment service-abort acceptance recovery

## Status
Accepted.

## Context

ADR-20261104144000 recovers acceptance after a lost reviewed default-scope
service-abort reply. Named aborts already use a full release review and native
protection guard, but their durable receipt recovery was unavailable. An
environment slug alone cannot bind evidence after deletion and recreation.

## Decision

Extend the existing exact reviewed service-abort attempt contract to named
environments. Persist canonical project/environment UUIDs alongside the original
scope, app, candidate/predecessor pair, fingerprint and reverse-handoff request ID
in the intent audit transaction. Default receipts keep the additive fields absent.
Fresh named attempts retain all existing release-review, ownership, predecessor,
configuration, route-policy and protection checks under the existing native locks.

Separate receipt visibility from duplicate detection. Owned deployment-read lookup
requires the retained app/candidate and matching current project/environment
identities. Deleted or recreated environments and moved apps cannot retarget old
evidence. Protection alone does not erase historical acceptance. An optional
attempt-store interface checks retained raw audit acceptance without disclosing
the receipt. Matching accepted retries and changed request reuse remain rejected
even when scoped reads are unavailable. The existing app/attempt unique audit
index remains authoritative; no migration or new store-wide interface is needed.

A separate encrypted named-service-abort reference namespace binds the complete
action identity, including project/environment UUIDs. Keep named references out of
the previous bridge's default-only namespace so it can preserve unknown references
through journal rewrites. New bridges apply normal grant purge and retention to
both namespaces. Save the opaque attempt atomically
before the original named POST. Restore and verify those references after expiry
or restart, including with writes disabled. Never backfill attempted legacy plans.
Recheck current read consent, immutable identities, retention and storage before
releasing results. History, resources and saved-plan prompts remain passive.

Return the original native request ID and an exact rollout observation call with
the original project/environment selectors. No read renews write authority,
changes history or retries an ambiguous mutation. Catalog counts stay at 58
read/planning tools, 67 with writes, eight prompts and 11 resource templates.

## Consequences

Named receipt reads can become unavailable while retained acceptance still blocks
reexecution. Missing evidence never proves non-commit. Acceptance remains separate
from gateway ACK, request drain, completion and current health. Service rollback
outcome recovery remains separate work. Native/provider execution qualification
cannot be inferred from portable fixtures or synthetic real-client receipts.

## Validation

Memory/PostgreSQL and native HTTP tests exercise concurrent attempts, protected
fresh intents, stale identities, lost replies, environment deletion/recreation
and retained duplicate rejection. MCP protocol tests cover durable-before-POST
ordering, restart/expiry, writes disabled, passive history/resource/prompt
discovery, named observations, mismatched receipts, revoked read consent and final
identity checks. Pinned Codex acceptance uses synthetic retained evidence and
performs no native writes or model requests. Go/Node/Python transports preserve
named identity fields while keeping write headers off reads.
