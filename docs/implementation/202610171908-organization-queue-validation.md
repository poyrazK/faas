# Organization queue validation

Local implementation of [ADR-202610171908](../adr/202610171908-organization-queue-ownership.md),
2026-10-17, using Go 1.26.9 and PostgreSQL 16.

The implementation retains queue, private consumer and environment runtime
identities, messages, receipt hashes and dead-letter history. Reservations
capture the organization's financial subject; adoption moves only the company
contributions of existing counters. The twenty reviewed organization operations
are available in OpenAPI and the Go, Node and Python SDKs.

Completed checks:

- The migration applies to a disposable PostgreSQL database and canonical
  schema generation succeeds.
- Memory/PostgreSQL state regressions pass with race detection: stable bindings,
  accepted messages, held receipts, stage runtime identity, retry and replay
  history through creator erasure; personal queue erasure; revoked request
  fences; quota adoption, shared cap boundaries and concurrent trigger admission
  across creator/organization namespaces. Unreviewed external triggers continue
  to block erasure. Related queue, receipt, offboarding and ownership regressions
  also pass.
- Backfill tests retain roots and counter contributions, reject mixed ownership
  and inconsistent counters, enforce immutable binding ownership and reject
  rollback while company reservations remain outstanding.
- Memory/PostgreSQL HTTP acceptance passes with race detection: an existing
  worker token and teammate acknowledge pre-erasure receipts and consume the
  retained backlog. A human enqueue paused after authorization returns 403 after
  removal and creates no message. Role/scope boundaries, organization aliases,
  aggregate usage and stage configuration remain enforced. Legacy trigger APIs
  cannot expose company projections through personal creator credentials.
- API clients, authorization, application access, lease service and account
  exports pass their component suites with race detection. All twenty-five
  queue action/role combinations and the complete action inventory are checked.
- Existing personal-account cap and reservation-release conformance cases pass
  in both stores. OpenAPI lint reports zero errors, with existing repository-wide
  warnings; route/schema parity and typed SDK coverage pass.
- Deterministic SQL generation and focused migration schema isolation and issued
  source adaptation checks pass. The disposable schema database was removed;
  the shared fixture database's public schema still has zero application tables.
- The complete Go SDK suite passes with race detection.
- Node: build plus 419 client tests and eight generator postprocessor tests.
- Python: organization queue, object storage and PostgreSQL client tests pass.
  Generation ran in an isolated temporary directory; only newly generated
  modules and the generated model barrel were copied back, preserving existing
  handwritten files and tests.
- Runtime documentation, ADR numbering, sealed-environment scope, text encoding,
  shell quoting, runbook SQL, durable-entity SDK contracts and whitespace checks
  pass. The previous workspace changes remain present.
- All configured Go static checks ran across the eleven changed packages.
  Focused context and export reruns pass after forwarding worker completion's
  authorization context and documenting intentional exclusion of malformed
  historical queue references.
- Focused race regressions pass after the context fix: worker serving receipts,
  pull completion and retained organization ownership. The pull completion test
  explicitly rejects a revoked authorization context before accepting the valid
  attempt; account export tests also pass.

Changes remain local. Production deployment and native KVM acceptance are
pending; the HTTP fixture creates a live worker record without booting a VM.
Async admission and dispatch must be paused during the schema change, with all
quota writers restarted together before resuming. Mixed old/new quota writers
are unsupported; rollback must precede creator erasure.
