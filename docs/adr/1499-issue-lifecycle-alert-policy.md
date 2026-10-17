# ADR-1499: Native issue lifecycle alert policy

Date: 2026-10-17

Status: Accepted

## Context

Native Slack alerts can select new issues and regressions and route their
channels, but cannot suppress unwanted environments or throttle these lifecycle
alerts together. Raw lifecycle webhooks also serve as an audit feed and must
continue to describe committed transitions.

## Decision

Add one app-scoped lifecycle policy with an allowlist of at most twenty exact
environments and a cooldown from zero to seven days. An empty allowlist permits
all environments; zero disables throttling. Revision zero returns these defaults,
preserving existing behavior. Continue to select events and destinations through
the existing native Slack policy. Use revision-guarded replacement, normalize
names in stable order and preserve revisions for identical saves.

Apply the filter to created/regressed native Slack queue admission only. One
receipt per original issue stores the last admitted lifecycle alert. Created and
regressed share the receipt across teams and channels. Under the existing app
lock, evaluate the latest receipt across canonical group members and commit the
receipt with activity and delivery. Missing routes, queue capacity failures,
ignored/snoozed captures, duplicate capture retries and previews consume nothing.
At exact expiry, another qualifying lifecycle transition may alert. Expiry alone
sends nothing. Channel, destination and policy edits never clear cooldown history;
changing the duration recalculates eligibility from the last admission. Merging
uses the latest member receipt; detaching a member restores its own evidence.
Thresholds, reminders, escalations, tracker notices and raw lifecycle webhooks
retain their existing independent behavior.

Expose read, revision-guarded update and read-only draft preview in REST, CLI and
Go/Node/Python clients. The dashboard preserves the draft while previewing. A
preview chooses a hypothetical created/regressed event for an existing issue,
resolves aliases and reads one repeatable-read snapshot. Its eligibility explains
only this policy's environment and cooldown checks; delivery still depends on
native subscription, ignore/response state, eligible routing, capacity and sender
checks. Preview never reserves a receipt, changes lifecycle or contacts Slack.
REST reads/previews require app read access and Issues entitlement; writes require
deploy-write and session MFA. The HTML policy editor requires deploy-write and
session MFA, with CSRF on its save and preview forms.

Policy edits affect future admission and do not recall already queued or in-flight
alerts. No automatic history replay occurs. Store configuration and operational
receipts in the clone schema registry without authorizing integration cloning.
Rollback refuses to discard active nondefault policies; restore defaults first.
All quotas live in pkg/api/limits.go. Migration SQL is additive and replay-safe.

## Consequences

Operators can reduce environment noise and repeated regressions without changing
issue history or audit integrations. Cooldowns measure queue admission, not Slack
receipt. There is one policy for both lifecycle triggers, not a compound-rule or
rate/anomaly engine. Existing per-rule Slack channel routing stays separate.

## Validation

Normalization and boundary tests; real PostgreSQL admission, suppression, exact
expiry, revision/no-op/concurrent saves, read-only preview, account isolation,
merge receipt behavior and replay/retry checks. HTTP entitlement/authentication,
stale writes and dashboard CSRF/draft checks. CLI and SDK contracts, generated
clients, fresh schema replay, SQLC parity and clone-schema coverage.
