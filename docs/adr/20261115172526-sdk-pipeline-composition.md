# ADR: Typed child pipeline composition over native workflow runs

Status: Accepted (SDK preview)

## Context

Typed pipelines compose Jobs and application decisions. Reusing a complete
reviewed import in a larger batch still requires handwritten child-run tracking,
concurrency, recovery and result verification. The native workflow engine
already owns durable runs, journals, pinned handler deployments and cancellation.

## Decision

Add opt-in single-child and bounded-batch calls to both pipeline contexts.
Register generated child contracts on a backend coordinator scoped to the same
app/account as the parent. Use native workflow start/read/cancel APIs and native
journal steps/sleeps; add no second workflow engine or component-owner bypass.

One application PostgreSQL row captures immutable input/contract/owner/policy
bindings and a database-clock deadline. A revision CAS admits each child under
the shared concurrency cap. Reserve before starting; only an acknowledged new
reservation can send the initial request. Unknown starts retain their slot and
need explicit verified-ID reconciliation. Child envelopes retain parent, batch
and item lineage in their input digest. Ordinary callers keep their existing
envelope/checkpoint shape; composed deployments use matching SDKs.

Observe typed native outcomes in input order. Capture separate policies for
failure admission, sibling cancellation, deadline cancellation and stopped-parent
cancellation. A bounded recovery pass observes and cancels accepted children
after parent exit/restart, with rotating row scans and no child admission.
Reuse existing application auth, gate metadata and live views for child progress.

## Consequences

Applications install the ledger, authorize parent ownership and operate recovery.
SDK bounds limit batch work; native plan quotas remain authoritative and unchanged.
Cancellation is eventually observed across services and cannot atomically prevent
an already in-flight start. Accepted Jobs remain independent of child workflow
cancellation. Stored typed terminal receipts support audit; unknown native runs
cannot be cancelled until reconciled. Failed native code workflows retain their
existing continuation limits. New APIs remain preview-only. Shared fixtures and
local PostgreSQL/Chrome tests demonstrate SDK behavior, not deployed acceptance.
