# Scheduled pipeline work validation

Scope: CUST-010–012 in
[the findings ledger](../../../pipeline-findings.md), on
`codex/pipeline-scheduled-data`, stacked on evolution commit
`ce6bb7f98746ac724afc3b6acdbd9d694bb141f5`.
The design and rollout boundary are recorded in
[ADR-20261114083000](../../../adr/20261114083000-pipeline-scheduled-imports-quality-and-work-budgets.md).

Validation uses disposable PostgreSQL 16 databases, isolated catalog schemas,
generated source/reader roles and the repository's digest-pinned TLS/PostgREST
fixture. It uses no live customer endpoint or production configuration. Local Go
commands use Go 1.26.11 and the current Makefile's shared development flags.

The scheduled regressions cover a SELECT-only, NOREPLICATION source with read-only
sessions; a nonlogical WAL fixture; no publications or slots; precise numeric/JSON
values; excluded fields; tenant isolation; atomic refresh preserving existing SQL
snapshots and relation identities; lost-ack receipt recovery without source access;
outage and row-budget rollback; deferred work, stale incidents and pause priority;
server-enforced query deadlines; older-worker fences; partial quality coverage,
quarantine, tenant-equal references and count-only reports. Quality alert tests
require a confirmed complete pass before recovering an incident.

HTTP tests cover revision conflicts, account isolation and work-policy round trips.
CLI tests use the public `main.Run` entry point and check malformed input before
network access. Dashboard rendering checks escaped results, CSRF tokens and frozen
revision fields. Migrated-capacity tests cover connection-only snapshot holds and
immutable import mode. OpenAPI parity and regenerated Node/Python SDKs preserve
the preview-only pipeline surface.

Check results and concise command output are collected beside this file as the
validation completes. The existing full race-enabled pipeline acceptance remains
available through `make test-data-pipeline`; local TLS acceptance in this evidence
uses the same pinned fixtures without the race build.

This is staging implementation evidence. Provider connection qualification,
live Neon faults/sleep/billing, production enablement, larger native COPY/index
workloads and review/CI of the complete stacked PR chain remain separate gates.
