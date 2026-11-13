# Pipeline evolution — 2026-11-13 UTC

This batch implements CUST-007–009 under
[ADR-20261113205500](../../../adr/20261113205500-pipeline-serving-evolution.md):
additive projection evolution, owner-approved Auth organization synchronization,
and declared related-table reads. Customer workflows are documented in
[data pipelines](../../../data-pipelines.md); findings and deferred work remain in
[the findings ledger](../../../pipeline-findings.md).

Qualification uses Go 1.26.11 and the current Makefile development flags,
disposable PostgreSQL 16 with verified TLS/native logical replication, and the
repository's digest-pinned PostgREST and Node gateway fixture. No customer data,
live provider account, production deployment or feature gate was changed.

## Results

- Complete native acceptance passed with `-race`: pipeline/catalog, HTTP domain,
  source gateway, dashboard, managed-database setup/capacity, bounded Neon
  credentials and pipeline CLI suites. The final pipeline run completed in
  654 seconds; every listed package passed.
- Final catalog and HTTP suites passed after the additional control-query fence,
  including the real PostgreSQL interleaving regression for PIPE-018.
- Schema regeneration and SQLC generation completed; migration expand/contract
  safety passed against the updated parent commit `658d74bd6`.
- OpenAPI lint and AST schema/route parity passed. Existing non-fatal OpenAPI
  warnings remain. Capability/SDK inventory checks passed; public SDK files
  remain unchanged.
- Node preview SDK generation/build and Python preview SDK generation/model
  round trips passed.
- Store, seal-domain, error-response, trimpath, runbook SQL, shell-quoting,
  text-encoding, documentation-link and ADR-number policy checks passed.

## Coverage

- Native projection changes preserve excluded fields through column expansion,
  bounded backfill, CDC, projected table additions and lost publication receipts.
  Source definitions need a separate exact approval before exposure.
- Auth synchronization reads authoritative organization memberships and user
  lifecycle state through the exact account-owned database binding. Tests cover
  removals, deleted/banned users, oversized snapshots, binding replacement,
  interactive app visibility versus background reconciliation, empty resets,
  source-independent revocation and expiration at SQL/HTTP/health boundaries.
- A signed JWT with the same subject from another verified issuer receives no
  rows. Private issuer receipts fence stale changes and survive acknowledgement
  replay. Admission/billing grace cannot renew or move grants across issuers.
- Related reads enforce invoker RLS and exact tenant equality, including when
  one subject belongs to both tenants. Native tests cover forged composite RPC
  arguments, recursive dependency quarantine, source-table reads, runtime refresh
  failures/retries, new application roles and generation cleanup.
- PIPE-018 has a catalog interleaving regression: a newly staged automatic
  grant fences an owner control based on an earlier reader snapshot, even when
  the configuration revision is unchanged.
- PIPE-017 has a retained native regression: revoking every tenant membership
  no longer prevents a later approved grant; quarantined tables stay closed.
- Catalog and HTTP journeys exercise frozen IDs/revisions, foreign-account
  isolation, old-worker claims, schema approval, redaction and recovery. CLI
  tests cover bounded strict files, action selection and immutable approval IDs;
  dashboard rendering covers escaped values and legacy contracts.

## Rollout limits

This remains an approved-account staging pilot. Apply the expand migration before
capable control-plane and worker binaries. Version 2 serving requires
`pipeline-evolution-v1`; older workers cannot claim it. Binary rollback retains
the forward claim fence.

Live Neon-source qualification still requires real staging credentials,
qualified allocations and reviewed provider fault controls. Local acceptance
does not establish live-provider or production readiness. Relationship support
currently targets complete primary keys (many-to-one/one-to-one); simple views,
one-to-many relationships, arbitrary transformations and larger workloads remain
outside this release. Auth revocation is bounded and eventual, with proofs
expiring within one minute, rather than instantaneous session invalidation.
