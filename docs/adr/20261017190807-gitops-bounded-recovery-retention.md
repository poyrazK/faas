# ADR-20261017190807: Bounded GitOps recovery artifact retention

- **Status:** accepted (implementation preview; native acceptance pending)
- **Date:** 2026-10-17
- **Related:** ADR-590, ADR-20261017190804, ADR-20261017190805, ADR-20261017190806

## Context

Recovery observations cannot ensure bytes survive until an operator selects a
rollback. Routing compatibility expiry and snapshot caching do not express a
bounded promise to preserve historical code and its provenance.

## Decision

Add optional `recovery_retention` to the reviewed environment manifest. Its
`releases`, `seconds` and `max_bytes` are mandatory positive integers. The
conservative initial ceilings are five releases, seven days and 1 GiB per source.
The sum of active source byte reservations is limited to 2 GiB per account.
Limits live in `pkg/api/limits.go`; database guards mirror them.

Approval takes effect immediately, including retained history. It fails if a
selected artifact has already entered deletion or its inventory is incomplete.
Omission/removal and explicit Git source detachment release this policy's
protection. Suspension preserves protection. Manual and protected-branch
approvals use identical fences. This storage intent grants no execution or
production enforcement authority, including on report-mode sources.

Select only the current binding's completed serving releases with code members in the same
account, project and environment. Order by original serving completion descending,
then release UUID descending. Within the age window, take the newest requested
count, including a successfully served active release. Protect the longest
newest-first prefix fitting the byte budget. A large release does not cause an
older smaller release to jump the queue. Charge each distinct main/sidecar layer
key once per release, with its maximum recorded size. Sharing between releases
is conservatively charged separately. Ordinary retained-layer storage accounting
still deduplicates physical keys. Shared runtime bases retain their existing
producer/GC rules; snapshots receive no new pins.

Use a database-derived reference inventory and the existing storage-key retention
lock. In an account with an approved policy, approval, serving publication and
metadata writers serialize exclusively on the account row, then lock keys in
sorted order. Every other account's writers take only a key-share lock on the
account row and skip inventory evaluation: deployment artifact writes, release
membership and app/project cleanup never block foreign-key checks, ordinary
account updates or each other, while a first approval's exclusive lock still
orders against them and its policy is re-read once granted. Guards scope the
inventory to the writer's account. GC rechecks references after acquiring the
same key lock. A claimed/deleted key cannot be revived. Memory storage uses its
existing mutex.
A small immutable first-serving journal is captured from completed serving receipts,
backfilled from the first known surviving completion at upgrade and cascaded
with its graph/source/release.
It preserves original completion independently of gateway acknowledgement rounds.
Current serving receipts retain their existing pending/served checks and lease
authority; resetting them neither erases history nor renews artifact protection.
The newest completed serving release has no age deadline while it is current.
A replaced release is selectable for the policy age after the next release first
completed serving, so a long-running release remains recoverable right after it
is replaced. Deadlines derive from the journal without a renewal worker: reads
do not write, and repeated receipts do not extend them. Count, budget and
reviewed policy changes may displace a release before its age deadline.

Protect release membership, completed serving evidence, graph identity, selected
deployment bytes, source/registry producer metadata and borrowed producer owners
from cleanup while selected. Source detachment must precede destructive source
cleanup. Retention does not freeze publisher revocation or current producer
selection: current trust and fresh target qualification remain rollback gates.
No database, secret value or application data restoration is implied.

Expose the effective approved policy, all-page charged usage, per-release
`protection_status`, `retained_bytes` and `artifacts_protected_until` through the
existing recovery catalog, CLI, dashboard and generated SDKs. Distinguish the
artifact age deadline from routing compatibility and current rollback eligibility.
`artifacts_protected_until` is omitted for the one protected current release.

## Validation boundary

Portable selection, state/GC, PostgreSQL transaction and surface tests cover
boundedness, replay and concurrency. These do not prove ext4 materialization,
physical VM rollback or leak acceptance. Exclusive native x86_64 KVM acceptance
remains required before production qualification/enforcement can be enabled.
