# ADR-1658: Native Slack issue self-assignment

Date: 2026-10-18

Status: Accepted

Extends: ADR-1496 (native Slack response actions and account links)

## Context

Native application-error Slack messages support acknowledgment and snoozing.
Responders need to take ownership without general app or arbitrary-assignment
permission. Existing links delegated only the older response actions; upgrade must
not silently extend that consent. A stale message must not overwrite a later
assignment, including ownership changing away and back to its captured value.

## Decision

Add a fixed Assign to me button to newly prepared native application-error,
reminder and escalation messages. Keep the existing signed message, workspace,
channel, policy, response revision, current source eligibility, account/MFA,
membership and narrow response-access checks. Self-assignment can target only the
currently linked actor. It grants neither arbitrary-assignee nor general app
access. Existing REST/dashboard assignment permissions are unchanged.

Store `can_assign` separately on message snapshots and app-scoped identity links,
defaulting both to false for retained rows. New message preparation sets the flag
once and retry never upgrades an existing snapshot. Explicit authenticated,
CSRF/MFA-protected browser confirmation delegates acknowledgment, snoozing and
self-assignment; show that scope beside the named Slack user/workspace. Existing
response-only links can still acknowledge/snooze but must confirm this expanded
scope before assigning. Renewing the same link consumes no extra identity slot.
Linking and replaying the original linking click never assign an issue.

Under the existing app lock, use the standard group-assignment transaction helper
with the linked actor as assignee and `assignment_source=slack`. Its normal actor
activity, canonical/member ownership, snapshotted `issue.assigned` webhook outbox
and message consumption commit together. Self-assignment leaves acknowledgment,
snooze, lifecycle and reminder policy unchanged. Already-owned issues are no-ops.
A callback reply failure can retry without repeating committed assignment.

Preserve one handling action per message. Assignment and grouping changes consume
all retained native button snapshots for the affected original/canonical issue in
the same transaction. This fences ownership/grouping ABA changes without resetting
response state. Old messages are never edited. A new queue, public endpoint,
SDK operation, Slack scope or credential type is unnecessary. No live Slack send,
deployment or VM lifecycle work is part of validation.

## Consequences

A newly linked eligible responder can claim an issue from Slack. An older link
requires consent once; revoked/expired links retain the existing recovery flow.
An assignment from a message consumes that message, so subsequent response actions
use Gregale or a fresh message. Current authorization and retained message context
remain necessary; possession of the delivery ID is never authority.

The additive migration is replay-safe. Rollback drops only the new capability
flags; ordinary assignment history and webhook receipts remain. Re-upgrade never
reconstructs consent. Clone inventory classifies these added fields with their
existing operational/account-identity tables, without enabling identity cloning.

## Validation

Real PostgreSQL: confirmed actor assignment, audit/outbox attribution, unchanged
response state, legacy message/link behavior, membership/identity/MFA and policy
revocation, stale response, ownership/grouping ABA changes, no-op, duplicate and
competing callbacks, atomic failure recovery, migration replay/down/up and clone
schema coverage. Unit tests bind the new action ID to the delivery context and
keep fixed privacy-safe messages. HTTP acceptance verifies signed callbacks,
CSRF-protected consent and retry after an ephemeral-response failure.
