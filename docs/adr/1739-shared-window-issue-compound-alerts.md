# ADR-1739: Shared-window compound Issues alerts

Date: 2026-10-18

Status: Accepted

Extends: ADR-1457 (grouping evidence), existing frequency alerts and native team Slack delivery.

## Decision

Add a separate revisioned compound-alert policy with up to ten named AND rules.
Each rule requires an event threshold and a verified distinct-customer threshold
in the same rolling (now-window, now] interval, optionally restricted to one exact
environment. Reuse the existing one-minute to one-day observation bounds and
one-minute to seven-day cooldown bounds; customer thresholds are 1–10,000 and
event thresholds are 1–100,000. Centralize the limits in pkg/api/limits.go.

Keep configuration and receipts independent of legacy frequency and fixed-24-hour
impact policies. Older readers cannot accidentally interpret a compound rule
while discarding its customer condition. Require the expected policy revision for
complete replacement, and preserve other named rules in dashboard/CLI edits.
Rule order and enable toggles preserve receipt identity; changing conditions or
timing begins a new cycle. Removing a rule prunes its receipts.

Count retained accepted occurrences and distinct authoritative customer identities
using one SQL query over the canonical issue and its linked members. Use exactly
the same timestamp bounds for both aggregates. Reported customer hints do not
establish identity. Later authoritative attribution contributes on the next
eligible capture; there is no enrichment-only notification trigger in this version.

Evaluate only after a unique capture inside the rule window, while holding the
existing app lock. Active ignore periods and snoozes suppress compound admission.
Persist activity, rule receipt, webhook transition and eligible native Slack queue
admission in the capture transaction. Duplicate capture retries cannot alert again;
any database failure rolls back capture and alert effects together. Expiry,
configuration changes and previews do not trigger notifications.

Use the distinct compound_threshold_reached activity/native event and
issue.compound_threshold_reached webhook event. Existing assigned/fallback team
routing, verified channel overrides, delivery identity, outbox retries and sender
eligibility remain authoritative. The existing frequency_rule_key routing selector
also matches compound rules when the event is compound_threshold_reached; event
type keeps identically named frequency and compound rules separate. A channel
override does not select a different team or change ownership.

A committed threshold transition consumes the rule cooldown, including when native
delivery is disabled, unrouted or at capacity. Webhook/native subscription and
delivery checks are independent; preview eligibility is not a delivery guarantee.
Existing queues retain their recorded evidence when compound rules change.

Preview one unsaved rule in a repeatable-read, read-only snapshot. Resolve merged
aliases, return every predicate, current counts, observation bounds, policy
revision and cooldown timing. Do not save, reserve receipts, enqueue or contact
Slack. There is no hypothetical new capture or inferred customer attribution.

Expose GET/PUT policy and POST preview endpoints, authenticated dashboard controls
with named CSRF/session/MFA protection, and CLI get/save/remove/preview commands.
Maintain OpenAPI and all Go/Node/Python clients. Register new tables as app
configuration and operational receipts in the clone schema inventory; this does
not make live delivery history cloneable.

## Migration and rollout

Apply data changes before enabling new apid behavior. Finish upgrading all apid
consumers before enabling compound policies or selecting their native event;
older workers cannot render the new alert kind. No feature is enabled by the
migration alone. Existing subscriptions and independent policies remain unchanged.

The migration is replay-safe, extends the existing event vocabularies while
retaining unrelated and later entries, and restores their previous shape on an
empty down/up round trip. Down refuses configured rules, receipts, retained
compound activity/outbox rows, native subscriptions or compound channel rules
instead of erasing evidence.

## Limits

This is a bounded AND rule, not an arbitrary expression language. Lifecycle
triggers, OR/nested conditions, traffic-relative rates, anomaly detection and
alternative actions remain separate follow-ups. Counts describe retained accepted
and verified evidence, not all failures or all users. Missing instrumentation,
rejected captures, delayed attribution and retention limits affect coverage.

## Verification

Five PostgreSQL regression journeys cover shared-window counting, verified-customer
deduplication, read-only previews, transaction rollback on outbox failure, eight
concurrent captures, duplicate retries, Slack queue claiming, revision races,
merge/split receipt scope, ignore/snooze suppression and regression snooze reset.
Migration coverage includes replay, refusal with retained configuration, and an
empty down/up round trip; it also refuses routing-only compound configuration.
A real PostgreSQL HTTP/dashboard journey covers plan
and credential boundaries, revision conflicts, CSRF and preview non-mutation.

Focused Go/Node/Python client contracts, Node builds, deterministic Node/Python
generation, SQLC parity, schema-dump tests, expand/contract checks and full OpenAPI
parity/lint validation passed locally. OpenAPI lint used the pinned binary and
all 53 configured rules with a longer local execution timeout; no rules were
skipped. Customer-attribution fixtures represent already verified platform
evidence; these tests do not establish live instrumentation or Slack service
availability.

All existing TestIssue* CLI journeys and generated-reference freshness passed.
Uncapped pinned lint reported zero issues across the CLI, dashboard, Issues logic,
alert-form and Slack packages, including their tests.

The large state/apid test packages used local test-only AST overlays to retain
the selected tests and their unchanged fixtures. Production files and repository
CI sources are unpruned. Full repository CI and production delivery remain release
validation; this change has only been prepared locally.
