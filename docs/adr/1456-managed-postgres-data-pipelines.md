# ADR-1456: Managed Postgres data pipelines

- **Status:** implemented for staging qualification; production disabled
- **Date:** 2026-10-16
- **Builds on:** ADR-650 and the Data API release work in PR #4809

## Context

A generated Data API makes an existing Gregale database accessible to an app.
Customers also need a managed way to copy their existing data into that database
and keep the copy current. Provisioning credentials or an API alone does not
provide initial loading, change capture, replication recovery, or verification.

## Decision

Start with one supported topology: PostgreSQL 16 public tables to a dedicated
managed PostgreSQL database, exposed through a SELECT-only Data API. Sources
must have primary keys, ordinary supported builtin columns, logical replication,
publication ownership, and a finite source WAL retention setting of at most 1 GiB.
Partitioning, source RLS, generated columns, arbitrary connectors, and writeback
are outside this contract. A JWT subject allowlist explicitly grants reads of
all selected tables; this is not per-row organization authorization.

The apid HTTP boundary owns account-scoped intent and age-sealed source URLs.
A separate `apid --data-pipeline-worker` process owns remote SQL and uses native
publications and subscriptions for initial copying and continued row movement.
Customer rows never traverse the control API. The worker's internal provider
adapter verifies the destination resource identity and uses private credentials;
apps receive only the existing managed read-only credential mode.

Catalog revision and lease checks fence stale observations. A destination session
advisory lock serializes remote side effects, followed by a fresh intent check.
Private destination receipts make nontransactional subscription creation
recoverable. A durable retiring marker precedes teardown, so a lost response
cannot re-enable a broken generation. Lost source WAL, missing target receipts,
schema changes, and mismatches require explicit owner resync. Resync replaces
only tables marked as owned by the preceding generation. Deletion removes native
replication resources and retains the mirrored data. Source cleanup must finish
before credentials, database, or account deletion can proceed.

RLS denies reads until every selected table completes its copy and replication
passes the initial barrier. Live status additionally requires an active source
slot within 16 MiB and a recent target receiver message. Observations expire after
90 seconds and must match the intent revision. Pausing stops replication while
retaining a readable snapshot; a blocked source does not imply fresh data.
Verification uses bounded primary-key-ordered digests and reports inconclusive
when the source changes, acknowledgement lags, or the size limit is exceeded.

Connection-budget reporting includes mirrored read-only bindings and marks native
replication demand unmodeled. It cannot establish a passing capacity guarantee.
All pipeline limits are declared in `pkg/api/limits.go`.

## Availability and qualification

This is internal, staging-only functionality. Both HTTP admission and the worker
require an explicit account allowlist and exact source host/CIDR policy in TOML.
Production startup refuses enabled pipeline configuration. The shipped service
unit is installed without enabling it; an operator must configure the pilot.
No live-provider or deployed-fleet qualification is implied by local acceptance.

`make test-data-pipeline` exercises actual TLS logical replication and signed
Data API HTTP requests in disposable containers, including concurrent source
writes, restart after a lost response, pause/resume, schema drift, resync, lost
WAL state, verification, and cleanup. Production qualification still requires an
isolated provider canary, resource/connection measurements, outage/WAL-retention
exercises, and an operational release decision before changing these gates.

## Consequences

Customers can onboard a source and optional read API with one CLI command. Native
PostgreSQL supplies copy/change ordering without a new queue or row-streaming
service. The first release requires a privileged source replication user and a
destination provider capable of native subscriptions. Schema evolution requires
an explicit new copy and may temporarily remove the old mirror tables. Additional
connectors and row-specific authorization need separate designs and evidence.
