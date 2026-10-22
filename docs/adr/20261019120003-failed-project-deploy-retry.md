# ADR-20261019120003: Retry confirmed failed project deployments

Date: 2026-10-12
Status: Accepted

## Context

Project source digests advance at build acceptance. A subsequent build or
readiness failure leaves the digest unchanged, so an ordinary reapply can skip
that workload. Resume observes original IDs and must not create new attempts.

## Decision

`gregale deploy retry SESSION_ID --tarball source.tar.gz --yes` reads the saved
project receipt and current deployment statuses. It selects only deployments
whose current status is failed, resolves their current project workload names,
and submits a fresh tracked project apply with exact --only selection. Successful
and in-flight workloads are excluded. Source must be supplied again; journals
retain no source or secret values. A new saved receipt precedes submission.

Directory retries accept --path and --source=auto|head|worktree, plus the normal
--worktree alias. Omitting source selectors uses the current directory with auto
policy. The CLI delegates packing, file exclusions, secret scanning and source
review to the existing deploy pipeline. Tarball and local source selectors are
mutually exclusive, checked before authentication or server requests.

The optional Project-Retry-Targets header maps app IDs to observed failed
IDs. APID validates account and project ownership, scope, exact selected workload
membership and latest failed status before reconciliation. It forces selected
unchanged apps into the build set. The targets enter the submission fingerprint.
Creation rechecks latest ID and failed status under the existing app lock in both
stores. Concurrent retries can accept at most one successor per target.

Retries use --no-triggers and do not accept secret or exclusion mutations. Current
HTTP schedules and secrets remain in place; retries do not replay stale saved
configuration. Protected environments retain normal scan and approval checks.
Current source is scanned and reviewed through the existing project apply flow.
The CLI refuses responses that omit requested retries or reuse failed IDs.
Updated APID is required for the guarded acceptance protocol.

## Consequences

This command retries confirmed build/readiness failures. Configuration conflicts,
cancelled or superseded attempts, unconfirmed admission and lost build enqueue
publication require inspection and are not inferred as retryable failures. Unaccepted workload rows
are excluded rather than guessing whether work was accepted. Older accepted sessions can retry their recorded failed IDs.

Deploy updated APID before publishing the new CLI. No schema migration or VM
lifecycle change is needed. A retry creates new deployments and fresh recovery
receipts; resume remains read-only.
