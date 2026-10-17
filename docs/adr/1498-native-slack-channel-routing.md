# ADR-1498: Native Slack channel routing rules

Date: 2026-10-16

Status: Accepted

## Context

Managed Slack installation provides verified team destinations. Owners need to
send production regressions and individual frequency alerts to different channels,
preview the outcome and identify the rule responsible for a delivery. A channel
choice must not create new team ownership or response permissions.

## Decision

Add an owner-only routing editor under Issues → Tracker sync notifications. Store
at most twenty ordered rules per app, with optimistic revision and owner/current
OAuth installation binding. Rules have unique safe keys, enabled status, one native
alert event, optional exact environment, optional frequency-alert key, optional
already-routed team filter and a channel ID. Only frequency alerts accept a
frequency key. The first enabled match wins; no match uses the existing team
channel. No glob, arbitrary predicate, broadcast or alternate team is introduced.
All bounds live in `pkg/api/limits.go`.

Choose the eligible assigned team or explicit native fallback using existing
routing before matching channel rules. Preserve captured recipients, ownership,
response scope and policy/assignment/membership checks. Rules apply to created,
regressed, impact-threshold and frequency-threshold activity alerts; native
reminders, escalations and tracker notifications keep their team destinations.
A rule cannot rescue a missing team route or enable a disabled alert event.

Use the connected workspace's sealed bot token to verify each distinct enabled
channel with Slack before saving. Require exact channel identity, current bot
membership and an active channel. Support bounded discovered channels and another
joined channel ID. Recheck routing/installation revisions and app ownership under
the app lock after external verification. No network I/O occurs in ingestion.
No-op saved rules preserve the revision, claims and queue. A workspace reconnect
or owner change leaves prior rules inactive until an explicit reviewed save.

Enqueue the selected channel, rule key and routing revision atomically with source
activity and existing queue admission. Changed rules cancel pending/failed/claimed
native activity alerts from the previous routing revision and release only their
leases. Preflight, manual retry and Slack response actions also require the
captured current routing revision; override messages additionally require their
owner and OAuth installation binding. Use indexed correlated policy checks rather
than additional joins in the shared delivery-source view, preserving the existing
response-callback query cost and two-second deadline. Existing metadata remains in retained history.
No previous alert is replayed or silently delivered to a replacement channel.
Already issued network requests cannot be recalled.

Key Slack threads and predecessor ordering by destination revision, issue/incident
and channel, preserving existing default-channel roots during migration. A root in
one channel cannot be used in another. A failed root blocks later posts only in
its own issue/channel stream, while the existing team-destination throttle remains.
Keep response buttons bound to the exact sent channel/message and retain the
existing account-link and current Gregale response authorization checks.

Preview and reorder use the existing owner CSRF/MFA/deploy-scope POST, preserve an
unsaved draft and never enqueue or send messages. Use the same pure first-match
function and database team-routing query as ingestion, and verify the selected
channel before displaying a preview. Show disabled events and missing team routes
explicitly. Actual delivery still applies ignore/snooze, queue and sender gates.
Expose selected rule, routing revision and channel in delivery history, with a safe
rule-key label in native Slack messages. No new public API or SDK surface is added.

## Consequences

Routing adds one bounded policy read per admitted native activity alert and up to
twenty pure comparisons. It reuses the existing installation, team destination,
queue, retries and retention. Edits do not reset reminder clocks or responsibility.
Channel membership can change after verification; delivery then reports Slack's
failure and never falls back to another channel. Rotating Slack tokens, Enterprise
Grid organization grants and routing response reminders by alert rules remain
separate capabilities.

## Validation

Pure matching/priority/environment/team/frequency-key checks and rule/form caps;
fake Slack channel verification with deduplication and rejection. PostgreSQL
preview/no-op/CAS/concurrent saves, owner/installation binding, default fallback,
captured history, cross-channel thread roots/replies, claimed-delivery fencing,
response access and reminder-clock preservation. HTTP editor CSRF, draft preview,
reordering, verified save, delivery history and linked Slack acknowledgment in an
override channel. Existing native delivery, OAuth, response, reminder and escalation
regressions; SQLC, spec and documentation checks. No VM or live Slack changes.
