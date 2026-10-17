# ADR-1575: Saved multi-service migration progress

## Status

Accepted for local implementation. Native lifecycle acceptance remains a release gate.

## Context

A customer moving a frontend and API needs one place to configure both services,
find accepted deployments after a lost response, and retry unfinished work. A
single app recovery pointer and the apply response cache cannot recover an APID
crash between deployment acceptance and response delivery.

## Decision

APID owns a creator-account-scoped project migration journal. Customers select
up to ten source-backed HTTP/GraphQL workloads from one migration-mode scan.
Existing reconcile, secret quotas/sealing, dependency ordering, deployment rate
admission and source enqueue remain authoritative. Container-image workloads,
workers, schedules and database transfer are separate workflows.

The client generates an operation UUID before submission and saves only that
pointer and optional source labels in navigation. PostgreSQL retains bounded
migration records, service dependencies, configuration key names and exact
reserved deployment/build identities. Tokens and plaintext values are excluded.
An internal keyed request digest fences changed create replays without exposing
a public dictionary hash of configuration values.

A versioned, project-scoped apply lease precedes every intent write. Every
selected service is configured before any build enqueue. After configuration,
APID saves app identities and deterministic deployment/build IDs before source
enqueue. The source owner recovers an accepted attempt without republishing its
source or creating duplicate work. A committed deployment without its build
reuses its original source and completes the existing queue publication. The
workflow's source upload preserves its deadline, so it cannot outlive the lease.

Progress reads exact identities and existing async deployment state. Resume
rechecks ownership, project, source checksum, latest default-scope deployment and
all normal admission policies. Accepted nonterminal deployments are skipped;
failed/cancelled attempts get a new identity. Resume names the exact reviewed
service set and journal version, fencing stale action replays and unrelated new
failures from expanding the operation. Configuration can target only
unfinished services. Every originally supplied key must exist in sealed metadata
before resume can enqueue, including keys the repository did not mark required.
An unaccepted attempt also pins the prior deployment so an unrelated deployment
cannot be silently adopted as migration progress. All selected apps are considered for enqueue even when
ordinary reconcile reports their source unchanged. A replaced/deleted app or
externally replaced deployment blocks automatic resume.

Verification/cutover summaries reference existing exact-deployment receipts.
Their last recorded status is historical metadata; reopening the existing owner
revalidates current deployment/configuration evidence. This journal does not
prove the whole dependency graph or switch traffic. Each production cutover
retains the existing explicit manual DNS actions and restoration acknowledgments.

## Consequences

Closing the console or restarting APID preserves deployment progress. A crash
before the final receipt update still leaves a deterministic identity to inspect.
Crashes before configuration completes require explicit resubmission of missing
values. Archives retain their existing one-hour lifetime; unfinished source work
requires re-scanning the original revision after expiry or loss of cache. The
new token must match the saved archive checksum and project. Completed source
queue publication remains durable independently of that cache.

Records are limited to twenty per creator account and expired unleased records
are pruned after ninety days. Up to eight deployment attempts are supported per
service. A long-running lease is visible in the receipt and prevents duplicate
project apply; stale leases expire after the existing project apply deadline
plus a persistence grace period. This is an orchestration receipt, not a new
scheduler, secret store or deployment admission bypass.

## Validation

Unit and HTTP tests cover lost response/restart recovery, source identity,
secret-free receipts, concurrent claims, configuration-before-enqueue ordering,
changed create replay and targeted failed-service retry. PostgreSQL tests cover
migration application, restart visibility, optimistic concurrency and the
project lease. Console tests cover independent service requirements, pointer
creation before POST, cache secrecy, saved progress and unfinished-only resume.
Native x86_64 KVM lifecycle gates must pass before shipping.
