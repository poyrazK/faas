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

The local TLS/native baseline passed: pipeline 591.015s, HTTP 28.758s, source
adapter 0.374s and dashboard 0.563s. After the final configuration/safety-deadline
fixes, the TLS snapshot/quality regressions and native verification window,
cancellation and one-batch-budget regression passed again (5.276s), including
quality-window rescheduling, staging without redundant target indexes and legacy
API control guards. The final
domain suite passed in 97.691s; HTTP/dashboard control checks also passed.
The public CLI, apidserver, clone-registry and migrated-capacity checks passed
(5.920s, 0.494s, 36.075s and 30.523s respectively). Source qualification is still
operator-reviewed; the fixture does not establish a live provider allocation.

The final migration was replayed into a fresh PostgreSQL database and dumped
again (111,195 schema lines); the migrated snapshot-capacity regression passed
again (24.796s). Trigger replacement is atomic and normal pause SQL remains
compatible with older API binaries. [Final schema/compatibility proof](schema-proof.txt).

Schema dump/migration and SQLC generation passed, along with OpenAPI AST parity,
Node generation/build, Python generation/import/DTO round trip, migration online
safety, test-store ownership, sealing/problem policy, text encoding, security-rule
retention, ADR numbering and trimpath checks. Concise command output is collected
beside this file. All changed runtime packages passed golangci-lint v2.14.0,
including CLI/server and the final pipeline changes. Existing server dashboard
CSRF/account-isolation journeys passed (1.195s).

- [Native/TLS baseline](native-baseline.txt), [final scheduled/TLS checks](scheduled-tls.txt)
- [Domain](domain.txt), [HTTP/dashboard](customer-controls.txt), [server dashboard](server-dashboard.txt)
- [CLI/server/clone/capacity](public-and-capacity.txt)
- [Core lint](lint-core.txt), [domain lint](lint-domains.txt), [final pipeline lint](lint-pipeline.txt), [generated lint](lint-generated.txt)
- [OpenAPI parity](openapi.txt), [migration online safety](migration-safety.txt)

The existing full race-enabled pipeline acceptance remains
available through `make test-data-pipeline`; local TLS acceptance in this evidence
uses the same pinned fixtures without the race build.

This is staging implementation evidence. Provider connection qualification,
live Neon faults/sleep/billing, production enablement, larger native COPY/index
workloads and review/CI of the complete stacked PR chain remain separate gates.
