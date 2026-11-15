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

## Merge qualification — 2026-11-14

PIPE-019 records the command-tree and help failures from
[Data API acceptance run 43618311957](https://github.com/poyrazK/faas/actions/runs/43618311957).
Canonical aliases preserve the existing command spellings. Evolution and work
controls now have manifest help, including creation's `--operations-file` flag.
The command-tree, usage/help, message, Markdown reference and pipeline CLI test
families passed locally. Approval and work-request regressions exercise both
spellings and retain their saved intent, action and revision.

The complete CLI run with the acceptance workflow's 25-minute package timeout
finished in 998.674 seconds. The pipeline discovery failures did not recur; it
failed `TestPoolExhaustionRealTCPSequenceAndCleanup` on three late arrivals and
`TestNativeTransportNegativeAssertionsRealTCP` on an unavailable connection
baseline. Both unmodified TCP tests passed on isolated rerun (10.45s and 28.30s).
The final hosted Data API acceptance remains the complete-suite merge gate.

[Hosted Data API acceptance run 43645387930](https://github.com/poyrazK/faas/actions/runs/43645387930)
subsequently passed on `6e616d7cc84c87b22826cc9b5cc52e0278fb36ae`, including
credential qualification, lifecycle and the complete CLI integration suite.
That commit also passed all six required ci-light checks and
[native pipeline acceptance](https://github.com/poyrazK/faas/actions/runs/43645388053).
The subsequent main integration replays the async-backlog migration and carries
the regenerated environment-branch preview SDK models. Its final commit still
requires fresh CI before merge.

The next integration separates the byte-identical expand migrations into
PR #7316 and uses its canonical schema in both feature branches. All six
required checks and native pipeline acceptance passed on
`7ac3626229dd8e8c91e538119c9402585136a01b`; its full CLI run exposed the
JSON-help typo-recovery regression tracked as PIPE-020. The parent feature
branch fixes it using the existing filtered suggestion helper. This branch
inherits that fix, regenerates SQLC and preview SDKs against current main,
and requires a fresh complete hosted acceptance run before merge.

[Hosted run 43667097190](https://github.com/poyrazK/faas/actions/runs/43667097190)
completed the complete CLI suite in 826.590 seconds on
`6615cb5b3ad2998b3c1873c39618f9197ba8175d`. Its sole CLI failure was the
offline-help regression in PIPE-020: live command discovery made four API
requests while producing typo suggestions. All six required checks, native
pipeline acceptance, SDK mirror and image checks passed on that commit.
The parent fix now supplies the existing offline manifest to the shared
suggestion algorithm. Focused JSON-help, typo/ambiguity and edit-distance tests
passed locally in 5.748 seconds, including the explicit zero-request recovery
hint regression. Final hosted acceptance remains required after restacking.

The corrected parent subsequently passed all six required checks and every
triggered acceptance, SDK and image workflow in
[run 43671412819](https://github.com/poyrazK/faas/actions/runs/43671412819),
then squash-merged as `10df47cfe18e85ca10d6351aa76f722385d552fd`.
This branch retains that exact main ancestry for final qualification.
