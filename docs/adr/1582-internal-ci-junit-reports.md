# ADR-1582: Bounded internal CI JUnit diagnostics

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1652, ADR-1657 and ADR-1578

## Decision

Add optional `junit` paths to each pipeline job. Each path is a unique literal
relative artifact path already selected by that job's `artifacts`. Normalize
and sort a cloned selector list into the immutable revision digest. Omitted or
empty selectors preserve the existing version-1 canonical definition and digest.
No report is inferred from a filename or collected for an unconfigured job.

Reports are diagnostics. Scheduler receipts, native stop confirmation and the
existing sealed result protocol retain execution authority. Reported failures,
missing files, invalid XML and parser limits never promote or fail a job, retry
a command or change the GitHub Check conclusion. A pipeline must return the
appropriate process exit code if tests should fail its execution.

## Collection and bounds

After native stop, the controller parses selected artifact bytes during existing
size/digest verification, with no additional provider read. Sealed output remains
mandatory on the production path. Result identity, attempt-owned object keys,
selectors, byte budgets and checksums must pass the existing checks. Provider or
checksum failures remain output collection errors; they cannot become accepted
parser diagnostics. Guests publish ordinary artifacts and receive no `junit`
selectors. Guest result JSON cannot supply report projections.

All bounds live in `pkg/api/limits.go`: two reports/job, 1 MiB XML/report,
5,000 actual test cases/report, depth 16, 100,000 tokens, 64 attributes/element,
20 retained failed/error cases, 256-byte names/classes/types, 512-byte messages,
1,024-byte details and a 32 KiB JSON projection/report. Oversized selected
artifacts are still verified by streaming before recording `limit_exceeded`.
Snapshots use the existing job-output and execution JSON, retention and account
ownership. No database migration or separate parser worker is required.

The streaming XML decoder has no network/file resolver, custom entity map or
charset reader. Reject directives and non-declaration processing instructions;
check cancellation between tokens. Support `testsuite`, `testsuites`, nested
suites and matching namespaces. Count actual `testcase` elements rather than
aggregate attributes. Ignore properties, stdout, stderr and unknown extensions.
One case contributes to one count: error precedes failure, while skipped and
failed/error together are invalid. Duplicate display names use case ordinals.
Strip control and directional formatting characters, clip on UTF-8 boundaries,
and bound escaped JSON by dropping trailing retained failures when necessary.
Exact totals remain even when failure detail is truncated. Invalid or excessive
XML exposes no partially parsed totals or parser error text.

## Durable reads and publication

Persist terminal reports with path, artifact digest/bytes, status, counts and
bounded failure details in the attempt snapshot. Execution/commit/job/attempt
identity comes from the owned execution. Freeze report data alongside artifacts
while draining terminal logs. Restart reads persisted output instead of reading
or parsing artifacts again. Configured absent artifacts are `missing`; legacy
terminal output with an artifact but no projection is `uncollected`. Both are
explicit incomplete diagnostics. Jobs without an attempt remain pending or
`not_run`; they do not invent report results.

`GET /v1/internal/ci/executions/{id}/reports` and
`gregalectl ci execution reports --id UUID` project these diagnostics without
provider access. Existing active account, allowlist, internal flag, admin, MFA
and HTTPS guards apply. Dispatch/worker/transport/GitHub flags may be off during
reads. Archives return the existing gone response for detailed reports and keep
only per-job aggregate totals in their bounded receipt.

Execution GitHub Checks add aggregate integers only: parsed/expected reports,
report problems and test/pass/fail/error/skip counts. Case names, paths, messages,
failure bodies and raw XML remain internal. Normal Check publication gates,
delivery ownership, versioning, retry budgets and redaction remain unchanged.

## Rollout and validation

Every production feature flag remains off and no release is authorized by this
change. Deploy all revision/execution readers before accepting definitions with
`junit`. Keep admission off during binary rollback: older readers do not support
the new definition member, immutable digest or report projection. Existing
definitions and guest manifests retain their prior format. There is no report
backfill or automatic re-execution of old attempts.

Portable tests use parser fixtures, real migrated PostgreSQL, fake native
receipts and fake managed storage. Cover parser attacks and limits, verified
artifact identity, single reads, native outcome authority, confirmed stop,
restart/log drain, aggregate-only Checks/archives, retention, API ownership,
HTTPS/authentication/MFA guards, CLI scoping and default-off configuration.
Native KVM and real storage/GitHub verification remain separate acceptance.
