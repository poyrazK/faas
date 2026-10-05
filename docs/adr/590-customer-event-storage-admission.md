# ADR-590: Customer event storage admission

- **Status:** accepted
- **Date:** 2026-10-05
- **Decision:** Bound retained customer event identities and logical JSON bytes
  per account at publication. Preserve retained duplicate acceptance at capacity.
- **Why:** ADR-589 bounds materialized delivery work. Saturated consumers can
  still hold pending event envelopes and recipient snapshots indefinitely.

## Budgets and storage units

`pkg/api/limits.go` owns `EventStorageLimits` and the retry polling interval.

| Plan | Retained customer events | Retained JSON bytes |
| --- | ---: | ---: |
| Free | 4,096 | 8 MiB |
| Hobby | 16,384 | 64 MiB |
| Pro | 131,072 | 512 MiB |
| Scale | 1,048,576 | 4 GiB |

These safeguards do not grant access to APIs otherwise unavailable on a plan.
Both pending and settled outbox receipts count. PostgreSQL charges the UTF-8
serialized JSONB envelope, the separate `event_data` copy, and the immutable
recipient snapshot. A generated checked column charges existing receipts too;
it follows any legacy snapshot capture or payload change. A partial covering
index supports account usage reads without fetching envelope JSON from TOAST.
MemStore estimates these same serialized values for portable qualification.

This is an outbox envelope and snapshot budget, not a physical database size
promise. Compression, indexes, event ledger retention, routing progress,
attempt history and invocation retention have their separate existing behavior.
Reserved `gregale.*` sources are exempt. Public ingress already rejects those
sources; trusted object and lifecycle producers retain their existing admission
paths and do not fail because a customer publication budget is full.

## Transaction and duplicate behavior

All Go store `AppendEvent`, `AppendEventWithTrace` and timestamp-preserving
`AppendEventAt` calls for customer `event.published` envelopes enter the new
admission path. Ordinary audit events and platform envelopes use their existing
writers. A customer publication holds the account row for share (against plan
changes), then locks its `event_storage_admission` mutex. It checks retained
identity before quotas. Matching duplicates append no ledger or outbox row;
different type, schema version or data retains the existing conflict behavior.
PostgreSQL compares data using JSONB equality, including equivalent numeric forms.
Normal public envelope, ownership and schema validation still applies.

New identities check retained count and bytes. The existing event trigger creates
the outbox and captures matching subscriptions in the same transaction as the
ledger insert. The final byte check includes that actual immutable snapshot;
exhaustion rolls back ledger, outbox and related trigger writes. Concurrent
publishers share the account mutex and cannot over-admit. Usage is calculated
from retained rows rather than a cached counter that could drift during pruning.
Admission scans the bounded account index once; the post-insert charge lookup
reads only the new identity. No event data is returned by the usage endpoint.

Plan upgrades take effect on the next admission. Downgrades keep accepted work
and allow identical duplicates while refusing new identities over the lower
budget. Storage rejection does not alter routing leases, recipient failures,
delivery retry budgets or accepted work. ADR-589 continues to route healthy
siblings while a consumer waits for live delivery capacity.

## Retention and API

Delivery completion does not release this budget. Settled receipts remain charged
for the existing thirty-day identity/recovery retention window and are released
by `PruneDeliveredPublishedEvents`. Pending receipts are never discarded to make
space. A customer needs retention pruning or a plan upgrade before admission can
resume at a full retained-storage budget.

`POST /v1/events:publish` returns HTTP 429 with stable code
`event_storage_capacity_exhausted`, the exceeded resource in the detail,
`limit`, attempted `observed`, a docs URL and `Retry-After: 60`. Byte exhaustion
also includes `limit_bytes` and `observed_bytes`. Sixty seconds is a polling hint,
not a promise of storage release. Repeating the same source and id after an
uncertain result remains the recovery action; retained duplicates return their
original receipt and acceptance timestamp even above a downgraded budget.

`GET /v1/events/storage` requires the existing read surface scopes and returns
only the authenticated account's retained count, logical bytes, pending count,
oldest pending acceptance time (null when empty), and current plan limits.
It uses `Cache-Control: no-store`. The Go client exposes `GetEventStorageUsage`.
Both OpenAPI sources document the endpoint and publication backpressure.

## Rollout and qualification

Apply the additive migration and upgrade customer event writers before relying
on enforcement. Direct operator SQL and old writer binaries do not use the Go
admission mutex; neither is a supported bounded customer ingress during rollout.
Existing over-budget accounts retain all receipts and get backpressure for new
identities. Platform production and the recipient adoption switch are unchanged.

Qualification covers a concurrent burst into the final account slot on memory
and PostgreSQL stores, duplicate receipts at capacity, conflict precedence,
absence of rejected ledger writes, byte rejection rollback, captured snapshot
charges, pending retention protection, settled retention charges, pruning,
upgrade/downgrade behavior, platform exemption, and account isolation. HTTP tests
cover the problem contract, stable duplicate receipt, usage response and resumed
publication after pruning. Existing routing and delivery-capacity tests protect
healthy-consumer progress and recovery independently of the publication budget.

The adversarial review checked snapshot growth beyond an envelope-only estimate,
concurrent final-slot publishers, duplicate/conflict precedence at capacity,
plan downgrade below retained usage, and pruning while accepted work remains
pending. The implementation charges the actual captured snapshot, serializes
admission, preserves PostgreSQL JSONB identity equality, and never prunes pending
receipts to regain capacity.
