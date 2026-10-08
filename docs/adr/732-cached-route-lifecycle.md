# ADR-732: Lifecycle guidance on cached responses

Status: Accepted

## Context

ADR-792 omitted lifecycle headers on cache hits because stored bodies did not
identify their serving deployment. Current rollout selection is insufficient:
the cached response may have come from a different production split member.

## Decision

Record the final origin deployment ID alongside the body, separate from the
cache key's deployment/cohort dimension. Preserve it in L1 and an optional
`served_deployment_id` field in the existing Redis v1 record. Older entries
remain readable without the field; never infer their origin from a key.

Fresh and all stale replay paths filter stored lifecycle headers and resolve
current metadata from the recorded deployment capture, using the original
public method/path. This reuses ADR-792 ownership checks, notifications and
TTL behavior. Capture updates change guidance without evicting bodies. Missing
captures and legacy records omit advisory headers while retaining the response.

Keyed writes and detached refreshes must use the selected deployment, never a
sibling. Pinned URLs retain their existing cache bypass. Stale replies wrapped
in an origin cache writer disable recapture to preserve identity and expiry;
otherwise the failed origin could be incorrectly recorded as the old body's
source. Clear failed-origin guidance before resolving the cached body's metadata.

## Consequences

This closes ADR-792's cache-hit publication limitation without changing cache
keys, traffic selection, retention windows or removal approval. Mixed-version
Redis readers ignore the optional field and legacy entries gain metadata only
on a normal origin refresh. There is no schema migration or new endpoint.
