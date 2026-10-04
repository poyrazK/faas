# ADR-479: Regional global release generation fences

Date: 2026-10-04
Status: Accepted; implementation in progress.

## Context

ADR-478 makes repeated acceptance of one release UUID safe. It cannot prevent
a delayed first submission of an older release from superseding a newer global
release that the region has already accepted. A coordinator lease alone cannot
fence a request that outlives its worker or lease.

## Decision

The regional image receipt PUT accepts an optional `ordering` object containing
`global_app_id` and `generation`. The global app UUID is canonical and nonzero;
the generation is a positive integer bounded to exact JSON integer precision.
The directory coordinator will allocate monotonically increasing generations
under its global app lock and persist them before dispatch. Every target of a
global release receives that same immutable identity, generation and release UUID.

The first ordered acceptance binds the regional app UUID to that global app
identity and its current regional owner. Each subsequent first acceptance must
have a strictly greater generation. An equal or smaller generation, a different
global app identity, or a different owner conflicts and creates no deployment,
activity or receipt. A matching receipt retry is checked before the generation
fence and returns its original acceptance even after a newer generation. Changing
an existing release's ordering is an input conflict, just like changing its image.

The regional app row serializes both the receipt and the generation head. The
head update, deployment intent, activity outbox and receipt are one transaction.
Database triggers enforce the same constraints against SQL bypasses. Head
identity cannot change; its generation only increases and its referenced receipt
must match. Heads cannot be deleted or truncated while their app identity exists.
Deployment collection retains both the receipt tombstone and generation fence.
Hard app deletion removes both. Downgrade refuses populated ordered receipts.

Ordering is authorized by the existing regional personal-account key and app
ownership checks; an arbitrary UUID or larger generation grants no access.
There is no implicit cross-cell authentication trust. Ownership transfer cannot
silently rebind an existing ordered stream; the global authorization/transfer
protocol must decide an explicit retirement and adoption procedure. The head
contains no key or secret and remains regional database backup state.

Unordered regional deployment APIs remain available for local intent. The
coordinator must always supply ordering for global releases. This protocol
orders accepted global intent; it does not allocate capacity, establish health,
publish ingress, or promise simultaneous cutover. Existing independently serving
regions still need no directory database on their request or wake path. The
default global serving policy remains `all_selected_regions`.

## Validation

Exercise delayed older submissions, equal-generation competing UUIDs, matching
old receipt replay, changed ordering, independent cells, concurrent increasing
generations, failed receipt rollback, ownership changes, SQL mutation/truncation,
deployment collection and hard app deletion. Use real HTTP APIs and all SDK
models to verify ordering survives serialization and redirects remain refused.
Coordinator allocation/reconciliation, global authorization/quota/ingress and
native KVM acceptance remain required for the complete multi-region feature.
