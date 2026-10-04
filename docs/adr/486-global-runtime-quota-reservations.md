# ADR-486: Global runtime quota reservations

Date: 2026-10-04
Status: Implementation in progress; regional admission integration pending.

## Context

Regional plan limits cannot each be granted in full to one global app. Capacity
must be partitioned across its selected regions, without a directory lookup on
the regional request or wake path. An unreachable region may still be serving;
an expired coordinator lease does not prove its capacity can be reassigned.

## Decision

The directory retains immutable, generation-fenced quota allocations for each
global app. Positive steady concurrency shares cover every selected, verified
region. One selected region receives the rollout grant by default. Both total
steady capacity and total peak capacity across all unreleased allocations must
fit the current tenant plan. Peak capacity includes rollout overlap, including
outstanding allocations from earlier generations. The canonical limits remain
in `pkg/api/limits.go`; an immutable policy digest identifies their snapshot in
PostgreSQL. Personal account and organization plans are resolved under locks,
with current owner/admin consent for organization allocation and retirement.

An allocation UUID identifies its immutable shares, rollout region and policy.
Matching retries return the same generation, including a retired tombstone.
Failed admission does not advance the head or leave partial target rows.
Allocation serializes on the tenant root and app. Registry generations and
regional tenant/app identities are snapshotted before reservation commits.

Before any regional authority can leave the directory, its target is marked
issued durably. Retirement releases an unissued reservation immediately. An
issued reservation remains charged until the regional protocol proves a durable
admission fence and zero counted instances for that immutable identity. The
acknowledgement includes the allocation generation, retirement UUID, original
cell/tenant/app binding and original API origin. Retry is idempotent. Detach,
registry changes, outages, expiry and owner deletion do not prove drain.

The acknowledgement store method accepts a trusted result from the future
regional verifier; it is not a customer-facing assertion endpoint. Reservation
and issuance bookkeeping do not themselves alter scheduler capacity. Regional
quota grant verification, local admission fences, organization metering/billing,
the public API/SDK/CLI and reconciliation still need integration before runtime
admission is enabled. Already issued capacity must remain durable during these
steps. Native KVM failure/recovery qualification is required for runtime changes.

## Validation

Run the same memory and PostgreSQL contracts for retry/tombstone identity,
steady and peak conservation, concurrent admission, issue-versus-retire races,
delayed and duplicate acknowledgements, stale registry/binding rejection,
tenant plan and consent changes, SQL bypass attempts and downgrade refusal.
Retirement of an issued target must retain its charge until an exact verified
drain acknowledgement; a lost response must never produce a second allowance.
