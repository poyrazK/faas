# ADR-421: Continuous app ownership recovery

- **Status:** accepted
- **Date:** 2026-10-01
- **Milestone:** M9 managed service recovery
- **Related:** ADR-064, ADR-137, ADR-193, ADR-420

## Problem

ADR-064 transfers apps away from inactive owners on a node-change notification
and once at scheduler startup. A lost notification, transient database error,
full destination or batch overflow leaves apps assigned to the inactive node.
ADR-420 only restores services owned by the local scheduler, so replica retries
cannot repair this missing ownership transfer. Repeating the same first page
also starves later apps when earlier apps never fit.

The old owner UPDATE fences competing schedulers but does not recheck node
health. A source that recovered or a destination that began draining after
discovery can therefore receive an inappropriate ownership transfer.

## Decision

Every node-owned scheduler scans at startup and every five seconds independently
of its notification subscription. Node-change notifications remain hints that
accelerate one source-scoped batch. One batch per Engine can run at a time;
overlapping hints are coalesced by refusing a second batch. The periodic scan
rediscovers any skipped work. The overall batch has a thirty-second deadline;
individual store and notification operations have five-second deadlines.

The durable work set is the apps table: non-deleted apps with non-admitting
owners, respecting the existing `reassigned_at` cooldown. Page by ascending
app UUID with an exclusive cursor and a bounded batch (default fifty). Advance
past capacity refusals, conflicts and individual transfer errors as well as
successes. Wrap at the end of a pass. Notification batches filter the source
before applying the limit and do not reset the periodic cursor. Restart resets
only this paging optimization; eligible app ownership remains durable.

The destination must be fully active and have a positive admission ceiling.
Account for existing residency and conservatively reserve one app's RAM plus
VM overhead within the batch. This is ownership admission, not a guarantee
that every desired replica fits: normal replica allocation still enforces
per-node RAM and CPU reservations and the database admission gate (ADR-193).

Use a new recovery-specific transfer instead of changing the generic transfer
used by pressure rebalancing. In Postgres, lock source and destination node rows
`FOR SHARE` in stable UUID order, then atomically update the app only when its
old owner, non-deleted status and cooldown still match. Source must remain
non-admitting and destination must remain fully active. Lifecycle updates
cannot commit between this health read and ownership commit. MemStore applies
the same conditions under its mutex. Competing transfers have one winner.

After commit, emit the existing `app_changed{rebalanced}` routing invalidation
and submit app recovery through the existing bounded service worker pool.
A missed or saturated submission is repaired by ADR-420's owner-scoped
service discovery. Request-mode apps remain dormant until requested; desired
service replicas zero remains zero. No controller invents a VM-death verdict:
the existing inventory and host-failure controllers still retire old residency.

## Qualification and operation

Store conformance covers exclusive scoped pagination, lifecycle/cooldown guards
and competing transfers. Postgres lock tests cover source recovery and
destination drain racing an ownership attempt. Portable scheduler acceptance
uses a fake VMM: sixty service apps lose their owner, surviving capacity is
initially exhausted, scheduler and store client are replaced, capacity returns,
and exactly the desired replicas recover without requests or notifications.
Fairness tests keep oversized early candidates blocked while smaller later
ones recover. Legacy rebalance behavior retains its admission cap and metrics.

No schema migration or customer API change is required. Deploy the scheduler
after ADR-420's service-recovery migration. Existing rebalance metrics and app
ownership rows expose outcomes; see [the operations guide](../ops/continuous-app-ownership-recovery.md).

Native x86_64 Linux KVM acceptance and leakcheck are required before fleet
rollout. This change supplies eventual ownership and replica convergence when
healthy capacity exists; it does not provision hosts, fence a physically
partitioned host by itself, or promise uninterrupted service during host loss.
