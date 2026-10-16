# ADR-1352: Environment cache policy history and reviewed rollback

Status: Accepted

## Context

ADRs 1324–1327 provide scoped policy replacement, inheritance restoration,
copying, transactional receipts and gateway convergence. Agents need a durable
way to inspect previous intent and recover an unsuitable change without
resetting revisions or overwriting a concurrent edit.

## Decision

Persist one immutable policy snapshot per positive app/environment revision in
an owner-keyed history table with cascading app/environment/account/project
foreign keys. Backfill current rows and available existing receipt responses.
Do not fabricate unavailable positive revisions. Updated apid writes history,
intent and retry receipt in one transaction; receipt replays append nothing.
The memory store mirrors cloning and deletion semantics. Retain history until
owner deletion, with bounded read pages (default 20, maximum 50) and descending
exclusive revision cursors. Read native history and current identity in one
repeatable-read snapshot, then recheck registry identity at the HTTP boundary.

Revision zero is synthetic initial inheritance, not a historical app rule
snapshot. Add native list and exact-revision GETs under cache-policy/history,
with the same named-environment selector, ownership, read access and MFA rules.
Historical rule bodies require exact MCP environment read consent. Recheck
immutable identity around each read and validate page order/cursors in MCP.

Add optional restore_revision to the existing native guarded PUT. Compare
historical inherited mode and normalized rules inside the destination write
transaction before a new mutation. Apply as current+1, never decrementing the
counter. Restore and copy pins are mutually exclusive; omitted restore pins
preserve existing request hashes. An old receipt recovers before revision or
historical-content checks, after current owner/environment identity checks,
so subsequent edits do not turn an acknowledged operation into another write.
Rolling back copied outcomes restores saved destination intent without needing
current authority over the old copy source.

Expose MCP history list, exact revision, rollback plan and rollback apply tools.
Bind the selected snapshot, current revision, immutable identity, desired mode,
rules and restore pin into the 15-minute review and encrypted journal. Apply
requires current destination write consent, including cached receipt recovery.
Distinct action plans cannot consume each other's apply tools. Inherited
rollback follows live app rules; it never freezes old app-owned rule bodies.
The resulting ordinary positive revision uses existing gateway reload,
revision-specific response IDs and convergence watching unchanged.

## Consequences

Agents gain a scoped recovery path without revision ABA or retry ambiguity.
History adds storage per policy write; pagination bounds response sizes rather
than retention. Migration can recover only extant receipt/current data. Upgrade
all apid writers after migration before promising complete new history. No new
gateway protocol or provider qualification is implied. App/environment deletion
removes history and prevents recreated slugs from recovering prior snapshots.
