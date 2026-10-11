# PostgreSQL starter recovery qualification — 2026-10-11

Scope: the embedded `rest-api-postgres` customer starter, based on main
`f06f07b55a`. No production rollout, provisioning enablement, or billing-plan
change was performed. The Neon fixture used Gregale's actual Neon adapter to
provision one disposable PostgreSQL 17 project, issue runtime/migration
credentials, replace and revoke the writer, and delete the project.

## Findings and fixes

The original starter exposed raw SQL errors in HTTP responses and crashed when
an idle PostgreSQL session was terminated. Both were reproduced using the
original main handler against an owned local database; see [before.json](before.json).

The updated starter handles idle and checked-out connection error events,
returns stable failure codes, and bounds SQL work and admission. Checked-out
clients need their own listener because pg-pool removes its idle listener while
the client is acquired. HTTP cancellation destroys the session before a later
COMMIT can run; writes are never automatically replayed. Connection failure
during COMMIT still has an uncertain outcome.

The source manifest now declares the existing ADR-348 loopback-only
`after_restore` callback. It replaces captured sockets and reads the released
workload within 1.5 seconds, inside the platform's two-second hook budget.
Failure keeps readiness closed. Missing schema fails readiness; an empty table
is healthy. Serving rejects migration credentials, and release connection
errors reach the sanitized migration CLI handler.

Runtime and migration URL parsing retain certificate and hostname verification
for managed SSL modes and reject alternative URL SSL overrides. Node's private
CA trust mechanism remains available. Explicit local `sslmode=disable` is
supported. Dependencies are locked for reproducible installation.

## Executed evidence

| Check | Result |
| --- | --- |
| Node helper, HTTP, and migration unit tests | 16 passed, zero skipped |
| Materialized starter SQL suite, PostgreSQL 16 TLS/SCRAM | 9 scenarios plus parent passed, zero skipped |
| Full templates and manifest-parser packages with Go race detection | Passed |
| CLI scaffold, starter upload, and managed binding manifest regressions with race detection | Passed |
| Real Neon direct and pooled endpoint qualification | 16 named checks passed |
| Real Neon active-session termination on both endpoints | 2 named checks passed |
| Workflow syntax and expressions (`actionlint -shellcheck=''`) | Passed |

The SQL suite tests the actual release and serving entrypoints, restricted DDL,
idle and active termination, HTTP cancellation, missing-schema readiness,
credential retirement, and unauthorized hooks. Both CI workflows explicitly
run the materialized SQL suite with Node 22 and a disposable TLS/SCRAM database;
the explicit invocation fails on missing fixture inputs. Ordinary unit runs
leave the destructive SQL suite opt-in.

The Neon checks use the exact current helper/app/migration source in local Node
HTTP instances. They cover concurrent writes after read-only pool readiness,
31-second idle eviction, session loss, hook replay without mutations, blocked
HTTP cancellation, forced compute suspension, and adapter-issued credential
replacement preserving all 16 notes. Active-session termination leaves no
committed test write and subsequent requests recover on both endpoints.

Warm session-loss hooks completed in 913–1,222 ms in the successful run. After
forced provider suspension, the direct hook returned 503 in 1,517 ms; a fresh
application instance/pool reached readiness in 1,539 ms. The pooled hook then
succeeded. This checks the SQL reconnection path used by cold boot, not an
actual guest cold boot. Earlier connection handshakes took 3,798–5,637 ms and a
session-loss hook hit its deadline; those observations are preserved in
[neon-connection-timing.json](neon-connection-timing.json) and
[neon-deadline-attempt.json](neon-deadline-attempt.json). The test did not relax
the serving or restore deadlines. Health polling and an explicit retry of the
known idempotent release are qualification-fixture behavior, not application
write retries.

Local golangci-lint exhausted its five-minute package-loading budget before
analysis; this is not a local lint pass. Full workflow shellcheck also reports
pre-existing warnings outside the added steps. CI lint remains required.

## Cleanup and remaining gates

[cleanup.json](cleanup.json) records revocation of all three issued credential
requests, adapter `ErrNotFound` and independent project GET 404, temporary key
DELETE 200, and authentication GET 401 using the revoked key. Private key,
one-time console capture, and SQL credential files were removed. Neon Launch
and production provisioning were unchanged.

These checks do not establish real VM snapshot restoration, scheduler retirement
receipts, gateway cold-boot fallback, public CLI rotation, or automatic Neon
idle suspension. The supported native acceptance runner remains a separate
gate described in [the deployed acceptance guide](../../managed-postgres-native-acceptance.md).
Existing deployments need the new starter source and lifecycle manifest to
adopt this callback. This evidence does not enable production provisioning.
