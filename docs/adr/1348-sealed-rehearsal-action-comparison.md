# ADR-1348 · Sealed rehearsal action comparison

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** Seal a bounded, account-owned inventory of captured managed webhook intents before fixture cleanup. Retain it with the durable capture policy and compare the two revisions by logical workload, operation/workflow source, effect name, destination fingerprint, event type and exact payload, preserving multiplicity.
- **Why:** Acknowledging capture configuration does not establish what happened. Generated receiver/operation IDs differ between isolated runs, mutable receivers can change after completion, and reading effects after cleanup can silently lose evidence.
- **Consequences:** Registration negotiates evidence version 1 before deployment. Completion records a destination configuration hash. Sealing refuses active managed work, serializes with completion, freezes membership and closes subsequent effect writes. Missing, legacy, oversized or unsealed evidence is inconclusive. Captured intent remains distinct from application-reported observations.
- **Rejected alternatives:** Comparing runtime IDs makes independent environments unequal. Collapsing repeated effects into a set hides duplicate notifications. Reading current receiver URLs misattributes committed intent. Comparing partial inventories as empty reports makes unsupported coverage pass.

## Snapshot boundary

`POST /v1/dev/test-runs/{run_id}/actions/seal` requires the account's existing
deploy-write authorization. It returns 202 with `status: pending` while managed
operations, workflows or invocations are active. An idle run with unchanged live
membership seals all its capture policies atomically. Run registration and
sealing share a transaction advisory lock; sorted app locks serialize completion
and sealing. A database trigger rejects later effect insertion or update even
from older workers that omit capture markers. Existing dispatcher suppression
continues after sealing and namespace cleanup.

The immutable snapshot is retained on one policy row. Repeated sealing returns
the retained evidence, including after expiry, soft deletion and namespace
cleanup. Physical purge of that anchor app/account removes its retained snapshot
through existing ownership cascades. This is an evidence retention boundary,
not permanent archival storage. Original effect rows can be removed by normal
cleanup without changing a retained snapshot.

The format supports up to 16 logical workloads, 256 captured intents, 512 KiB of
aggregate stored payload, and a 1 MiB wire document. These are protocol bounds,
not plan entitlements. PostgreSQL's JSONB whitespace counts toward the stored
payload bound. Overflow or missing capture/fingerprint data seals an explicit
incomplete inventory without a misleading partial list. An old `operation.effect`
ledger row without a matching captured effect also makes the snapshot incomplete.
Suppressed ordinary lifecycle events are counted separately from business intent.

## Stable destination and comparison

At completion, normal destination authorization and receiver locking precede a
SHA-256 fingerprint over a versioned JSON array containing receiver scope, tenant
external reference (where applicable), exact target URL, delivery format and
retry policy. The snapshot exports this fingerprint without the URL or signing
secret. It does not attest receiver signing-secret identity or target behavior.
Different physical app, receiver and operation IDs do not affect equality.

`gregale test rehearse` and `gregale test compare` compare sealed snapshots when
action capture is requested. The comparison is bound to the same trusted recipe,
observer files and fixture hashes as the declared behavior comparison. The
runner collects intent before fixture cleanup with a separate 10-second bounded
deadline; a cancelled or failed execution still attempts collection. Collection
failure fails that run. Failed execution or cleanup makes equality inconclusive.

Within each logical effect group, destination fingerprint, event type and
canonical JSON payload form a multiset: object property order and equivalent
JSON numeric spelling are ignored; array order, exact values and duplicates are
preserved. Console, JSON, HTML and Markdown report action evidence separately
with provenance `gregale_captured_managed_webhooks` and scope
`managed_operation_webhooks`. An action difference or inconclusive inventory
fails the comparison command even when declared observations pass.

## Supported scope and qualification

This boundary covers committed webhooks from managed HTTP operations and managed
workflow steps. It does not intercept direct workload HTTP/SMTP/payment calls,
shell commands on the CLI host, or arbitrary integrations. Passing this scope
does not establish complete external-action coverage.

Existing egress allowlists retain platform, private-service and operator
exceptions, and empty allowlists retain normal public access. They cannot serve
as a complete rehearsal isolation policy. Direct outbound containment requires
a durable admission policy acknowledged by every participating runtime, closure
of exception paths, explicit allowed dependencies, and dedicated native KVM
qualification across cold boot, resume and policy recovery. That runtime change
is a separate increment; this decision does not claim containment.

Portable acceptance covers memory and PostgreSQL sealing, active work,
completion races, retained receiver identity, cleanup, late/legacy writers,
incomplete inventories, duplicate intent, reporting and capability negotiation.
Hosted VM qualification remains a separate acceptance gate on native KVM hosts.
