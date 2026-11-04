# ADR-20261103230000: Pipeline safety controls and retirement refresh retry

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-03
- **Amends:** ADR-1591, ADR-20261103220000

## Context

Opening the sealed source URL before reaching the engine prevented approved
reader revocations and subscription suspension when an earlier encryption key
was unavailable. Billing grace kept existing pipelines active while rejecting
reader reductions through the deployment gate. A terminal runtime refresh during
table retirement also had no explicit retry: replay reused the failed wake ID.
Source discovery accepted read-only sessions that could not create publications.

## Decision

When source lookup or decryption fails, use the same private destination access,
session advisory lock, current lease/revision checks, subscription ownership
validation and policy receipts as ordinary reconciliation. Apply approved reader
changes without source access. Disable all owned unfinished subscriptions for
pause, disconnect or lost account admission. Preserve required resync state.
Disconnect remains deleting until source cleanup succeeds; capacity is retained.
Keep ordinary source-available reconciliation and its publication ownership checks.

Accounts still eligible for existing pipelines may preview and approve strict
reader reductions without deployment permission. Retain the one-reader minimum.
Determine approvals from the saved exact before/after sets, recheck eligibility
at approval and retain revision/hash/generation fences. Grants and replacement
subjects require deployment permission. Discarding a preview requires only
existing-pipeline eligibility. Keep these reader controls visible during grace.

An approved, archived table retirement blocked by a terminal API refresh may be
retried through its existing exact approval action. Persist an optional integer
RefreshRetry in cancellation metadata. Exclude it from the retirement identity,
so approved scope, private receipts and existing zero-attempt IDs remain stable.
Use it as the durable refresh attempt when deriving wake IDs. Bound it by the
existing ten-retry limit independently of schema/copy attempts. Require the
current revision, fresh failed observation and no outstanding reader approval.
Do not repeat withdrawal, archive or copy. The same rule covers onboarding
cancellation and live-table removal. No catalog migration or native receipt
schema change is needed; omitted counters retain their existing zero value.

The shared source inspector requires a writable primary and read-write sessions.
Discovery and worker preflight report source_writable_required with remediation
before publication or slot creation.

## Validation and scope

PostgreSQL tests exercise admission changes between preview and approval, caller
subject substitution, exact account/revision scope, retry exhaustion and lost
worker fences. Native TLS/password tests confirm actual RLS revocation, owned
subscription suspension without the source key, admission suspension, retained
publisher cleanup, ownership rejection and recovery after credential rotation.
Both retirement kinds recover from a dead-letter runtime refresh with one new
wake ID per explicit attempt and unchanged remaining rows. Dashboard rendering
checks grace-period reader controls and approved retirement retry scope. The
full native acceptance suite, race detector, generated SQL and repository checks
remain required. Production and provider qualification gates are unchanged.
