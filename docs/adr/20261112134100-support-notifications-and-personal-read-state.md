# ADR-20261112134100: Support notifications and personal read state

## Status

Accepted.

## Context

The native support portal and staff inbox need to surface replies and assignments
without requiring customers and agents to repeatedly check every conversation.
Notifications must preserve the personal/organization and customer/staff boundaries
of ADR-20261111130000. Mail outages must not require resubmitting a conversation.

## Decision

Support writes create metadata-only notification rows in their existing PostgreSQL
transaction. The unique event/recipient/audience key and existing mutation receipts
prevent duplicate fanout. A customer's initial message, reply or reopening reaches
the assigned staff member; unassigned work reaches current team members and managers
(including eligible bootstrap managers). A changed assignment notifies its new
assignee. Staff public replies, reviewed engineering updates and staff reopening
messages reach the personal owner or current organization owners/admins. Actors
receive no notification about their own action. Internal notes generate no customer
notification. Conversation text and subjects are never copied into notification rows
or mail payloads.

Read state belongs to an account and a customer/staff audience. APIs and browser
forms mark only that actor's existing notifications through an observed sequence.
A later update remains unread, including when it arrives while a page is open.
Read updates are intrinsically idempotent, do not produce permanent mutation
receipts, consume no conversation-write quota, and advance no ticket revision or
service clock. Browser pages acknowledge automatically only after the last message
page is rendered; a normal form also works without JavaScript. GET requests remain
read-only.

Own read-state updates require current authorization and a trusted origin. Browser
forms also require the existing account-bound CSRF token. Staff reads and read-state
updates require an MFA-completed session and current roster/bootstrap access, while
conversation and configuration mutations retain their fresh five-minute step-up.
This narrow exception avoids requiring a fresh authentication code simply to
acknowledge an update; callers can never change another person's read state.

A bounded worker uses the platform's configured mailer with a stable per-notification
MessageID, lease fencing, backoff and at most eight attempts within 24 hours of the
event. The mail transport's provider idempotency contract covers repeated handoffs;
delivery is not claimed to be exactly once. Before every handoff, the worker
rechecks account verification/status, organization membership or enrolled staff
access, current assignment, and customer preview enrollment. Already-read and
ineligible notices are skipped. A SHA-256 destination binding prevents an ambiguous
handoff from being redirected to a changed address on retry. This digest is metadata,
not an authentication credential. Mail links use the configured HTTPS control-plane
base and require authentication to read the conversation.

## Consequences

The additive migration must precede apid. The notification table is operational
data and excluded from environment clones. Notification history is retained for
90 days, independently of conversations and audit events. Failed or expired email
does not mark an in-app update read. The existing signed helpdesk webhook remains
independent. Attachments, incoming email, preferences, deadline alerts and escalation
remain separate work.
