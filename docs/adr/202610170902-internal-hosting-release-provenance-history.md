# ADR-202610170902: Internal hosting release provenance and promotion history

Date: 2026-10-17
Status: Accepted for the internal, default-off pilot

## Context

Staging, immutable private previews and revision-bound promotion let operators
review release bytes. They still need source context and a durable explanation
of accepted promotions and rollbacks. Existing audit emission and first-promotion
markers are not a complete deployment timeline.

## Decision

Add optional immutable caller-provided repository, full Git SHA, branch and
description fields beside the existing create-release manifest request. Store
these only in PostgreSQL release metadata, never published manifests or files.
Treat them as unverified labels: no repository fetch, attestation claim, clickable
source URL or authorization decision. Enforce byte bounds and reject control and
format characters, invalid UTF-8, abbreviated/uppercase Git hashes and empty objects.

A new independent default-off API flag admits metadata, records accepted
promotions, and exposes history. When enabled, the existing promotion transaction
also inserts an event with the prior/new release IDs, prior/accepted revisions,
authenticated principal account/key identifiers, database timestamp and target
source snapshot. Failed CAS, history insertion, pruning, outbox or transaction
commit cannot partially accept a promotion. Memory mirrors this under one mutex.
No client field can select the actor. Rollback uses the same promotion path.

Capture only enabled API promotions; legacy/off calls retain their existing
behavior. Do not fabricate backfill or claim history completeness across disabled
periods/older writers. Preserve existing first-promotion semantics separately.
History records accepted intent, not origin publication, regional convergence or
serving health. Existing audit events continue independently.

Bound retained events to the latest 1,000/site, pruning in the promotion
transaction under the site lock. Serve 50-event pages with exclusive revision
cursors. Old snapshots outlive physical release/key deletion; they are not FKs to
those resources. Site purge cascades its history. Downgrade refuses while any
source metadata/history remains; archival and draining are explicit operations.

## Consequences

The dashboard links source labels, previews, retained/cleaned-up releases and
history to the existing read-only comparison, never directly to rollback. Source
forms and history panels also need a separate default-off build flag and API
capability. CLI source options/history require an explicit workstation flag.
Source admission is checked before idempotency replay and again on execution.

Upgrade every writer before enabling capture to avoid avoidable gaps. No Worker,
DNS, Access, CDN or provider operations change. The feature can be qualified
locally using memory/Postgres concurrency, transaction failure injection, API
permissions/replay gates, CLI contracts and browser workflows. See the
[operator guide](../internal-frontend-hosting-history.md).
