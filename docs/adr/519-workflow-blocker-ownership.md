# ADR-519: Application-reported workflow blocker ownership

## Status

Accepted for the customer Operations HTTP implementation.

## Context

ADR-518 retains blockers and source-linked resolution facts. Operators need to
understand who should handle blocked work and what the application recommends
without moving business authorization or execution into the control plane.

## Decision

Add optional public `owner` and `next_action` observations to each blocker and
optional `resolved_by` attribution to each resolution. Use the existing scoped,
revisioned state-report protocol, full-list replacement semantics, transaction
outboxes, retained source checks, and history. Assignment updates do not change
business state or reset the original observation age of an active target/code.
The application authorizes assignment and locks its own authoritative rows.

Bound actor identifiers to 128 UTF-8 bytes and action guidance to 512 bytes;
reject invalid UTF-8 and control characters. Empty ownership means unassigned.
These fields are customer-visible observations, not verified identities,
platform access grants, executable commands, or evidence of a business effect.

Show current assignments in attention and detail views, retain blocker snapshots
in workflow history, and show attribution on explicit resolution facts. Clearing
a blocker without such a fact remains distinct from explaining its resolution.

Queue and summary reads accept an exact owner or an unassigned-only selector.
Ownership, code, and target filters must match the same blocker. Preserve full
queue snapshots, while owner-selected summary counts and ages include only
matching blockers. Owner groups use the empty public value for unassigned work;
keep totals separate from that group and distinguish initial pagination from
continuation after the empty group. Cursor fingerprints include ownership
selectors. Shared workflows may contribute to several owners without duplicating
the unique workflow total. Existing scoped access and retention remain in force.

### Escalation policies

Versioned workflow steps can declare at most 32 policies by blocker code. Each
policy has a positive threshold of at most ten years in whole seconds and a
bounded public recommended recipient. All steps in one definition agree on the
map. Evaluate against the current retained report's pinned definition and known
first observation at the attention cursor time. Do not infer missing ages or
escalate terminal workflows. Policies change through new workflow declarations,
not operator edits to the observation queue.

Expose passed thresholds as separate read-only findings, escalated-only queue
filters, and workflow/blocker counts in owner summaries. Combined ownership,
code, and target selectors match the same escalated blocker. Recommendations do
not change assignment, contact anyone, authorize a business action, or execute
an effect. Existing revision, access, retention, and pagination rules remain.

## Consequences

Existing reports remain valid and no schema migration is required because
blockers and resolutions are stored as JSON. Go, Node, and Python transaction
helpers retain and validate these fields. Older writers can discard new fields
when replacing snapshots; applications must upgrade all writers before relying
on assignment continuity. Standard scope checks and retention rules apply.


### Blocker acknowledgement and follow-up

Applications can report `acknowledged_at` and `acknowledged_by` together on an
active blocker, with an optional `follow_up_at` deadline. Actor identifiers use
the existing public actor bounds. All times are finite RFC3339 timestamps;
acknowledgement must follow the known first observation and cannot be later than
the report. Follow-up must be at or after acknowledgement. A past follow-up
is valid and immediately overdue at the queue evaluation time.

These fields are application observations, not verified platform identities.
Publish the full replacement blocker list through the application's business
transaction and outbox. Preserve acknowledgement metadata on subsequent updates;
clearing it explicitly returns the blocker to the unacknowledged queue. Upgrade
all writers before relying on these optional fields.

`reason=unacknowledged` selects blockers without acknowledgement;
`reason=follow_up_overdue` selects deadlines reached at the fixed queue evaluation
time. Owner, code, target and reason must match the same blocker. Summaries expose
`unacknowledged_blocker_count` and `follow_up_overdue_blocker_count`, including per
owner groups. Older blockers with no acknowledgement fields count as
unacknowledged. Refresh the queue to observe newly elapsed follow-up deadlines.

Acknowledgement neither resolves a blocker nor resets its first observation or
escalation threshold. Existing revision history retains acknowledgement changes.
No notification, reassignment or business action is performed by Gregale.


### Business impact and priority queues

Applications may add `priority` (`low`, `normal`, `high`, or `urgent`) and
`business_impact` to each reported blocker. The impact is public UTF-8 text,
limited to 512 bytes without control characters. Omitted priority is treated as
`normal` for queues and counts. The application determines business urgency;
Gregale does not infer urgency from customer identity or financial information.

For example, a shipping blocker can report `priority: urgent` and
`business_impact: Order cannot ship; customer promised delivery tomorrow`.
Set the workflow's existing `deadline_at` to the promised business deadline.
Publish this metadata through the normal application transaction and outbox,
preserving the entire replacement blocker list and its acknowledgement fields.
Priority changes preserve blocker age, escalation timing, and acknowledgement.
They grant no execution authority and trigger no notifications.

Use `priority=urgent` on attention queues and summaries. Priority, owner,
unassigned, code, target and blocker-specific attention reason must match the
same blocker. Workflows with no blockers do not match a priority filter. Queue
entries still return their full reported blocker snapshot. Summary totals and
per-owner groups expose `low_blocker_count`, `normal_blocker_count`,
`high_blocker_count`, and `urgent_blocker_count` for the selected blockers.

Use `sort=deadline` to list the earliest workflow business deadline first;
workflows without deadlines appear last. Equal deadlines use descending update
time and workflow identity as deterministic tie-breakers. `sort=updated_at` is
the default. Summary groups keep their existing alphabetical group ordering.
Cursors bind both priority and sort order as well as the existing access scope
and filters. Refresh from the first page after changing filters or metadata;
this is an evaluated retained-state view, not a database snapshot across writes.

SDK transaction helpers, CLI flags `--priority` / `--sort`, dashboard filters,
and revision history retain and display this application-reported context.
All writers must preserve these optional fields before relying on them.


### Verify cleared blockers with retained business evidence

Clearing a blocker and confirming its resolution are separate observations.
Applications can opt a resolution into verification by reporting paired
`verification_milestone_id` and `verification_milestone_name`, with an optional
`verification_operation_id` and `verification_owner`. The evidence Operation
defaults to the Operation publishing the resolution. Allocate an exact milestone
ID in the application transaction; publish it with the business confirmation
(e.g. `payment-confirmed` or `inventory-reserved`) in the same transaction or a
later publication. If confirmation will occur in another Operation, report that
Operation's exact ID as well. These public owner identifiers use the existing
128-byte actor limits; they do not verify platform identity.

Gregale checks the exact Operation, milestone ID and name. Evidence must be
retained in the same account, app, customer, environment, business subject,
workflow instance, and the resolution report's contract version. Its pinned
workflow mapping must identify that instance. Matching a milestone name alone,
a fact in another customer's workflow, or a fact in another contract version
cannot verify the resolution. This is evidence presence, not independent
validation of the application's business conclusion. Applications decide whether
the chosen fact establishes a fix.

Until matching proof is retained, the finding is `awaiting_verification`; with
matching proof it is `verified`, with `verified_at` equal to proof publication
time. Absence, expiry, or a mismatched reference remain awaiting verification.
A resolution without verification fields remains a legacy resolution and does
not create a new obligation. The existing source-report ownership and revision
checks continue to apply when admitting resolutions.

Obligations come from retained resolution history, so later state reports and
terminal workflow states do not silently clear them. Identical repeated claims
collapse by original blocker occurrence and exact evidence reference; the earliest
revision supplies immutable verification ownership. Different evidence references
are distinct claims. Use an accurate reference and owner when publishing the
resolution. Upgrade all writers before relying on these optional fields.

`reason=awaiting_verification` selects pending obligations and accepts the same
owner/unassigned, blocker-code and target filters against one resolution. Pending
ownership is `verification_owner`, separately from the active blocker owner or
`resolved_by`. Priority filters continue to select active blockers; pending
obligations have no priority and do not match a priority filter. Default attention
queues include retained workflows with pending verification even if otherwise
unblocked or terminal. Summary totals and owner/code/target groups expose
`awaiting_verification_workflow_count` and
`awaiting_verification_resolution_count`; cleared blockers do not become active
blockers again in these summaries.

Attention entries and selected workflow-instance views expose a preview of up to
16 findings, with matching pending items first, and exact
`awaiting_verification_count` / `resolution_verification_count` totals over all
retained distinct obligations. Selected workflow-instance history pages include
`resolution_verifications` for each report so older obligations remain inspectable
beyond the preview. Queue evaluation time freezes which published claims and
proofs can participate across cursor pages; refresh to observe newly published
confirmation. Retention and state changes still make this an evaluated retained
view rather than a transactionally frozen database snapshot.

The dashboard and CLI display proof identity and status. Pending proof marks the
observational workflow decision as needing attention, including terminal workflows.
It does not re-block an Operation, change workflow state, grant action authority,
or alter readiness enforcement. No notification or business action is performed,
and the existing JSON storage needs no migration.

### Retained workflow bottleneck analytics

The selected workflow snapshot now includes bounded bottleneck analytics independent of history pagination. Both stores share one interval calculation over the latest 1,024 retained reports, with at most 32 rows per breakdown. Consecutive, unambiguous revisions supply state and blocker intervals using occurrence time; terminal states stop accrual. Blocked totals measure the union of intervals, while per-code/Operation/owner groups may overlap. Contract versions remain separate, and uncertain boundaries are excluded with explicit coverage reasons.

Resolution verification uses publication time, preserving the original obligation owner and reporting unknown starts when the original resolution report is unavailable in the window. The dashboard, CLI and SDKs expose coverage and truncation alongside durations. Analytics remain observational and do not change admission or workflow decisions.

### Cross-instance workflow performance summaries

Performance summaries reuse the retained interval calculation across separate completed and ongoing cohorts for one app/environment/workflow. The latest 100 retained instances per cohort are sampled by current report update time, with all-matching counts and explicit sampling flags. Incomplete histories remain in coverage counts and are excluded from every duration distribution, preventing inferred durations from shifting comparisons.

Distributions use nearest-rank p50/p95 and one accumulated duration per eligible instance. Overall distributions include eligible zero values; dimension groups include only instances that reported the group. Group output is ranked by total seconds and capped at 32 without changing scalar totals. Contract versions remain distinct for states and blockers. The dashboard and account/customer read APIs expose coverage with metrics. PostgreSQL uses one statement to select bounded cohorts and read scoped history and verification evidence; the in-memory store holds its lock through sampling and aggregation. No workflow decisions or admission policies change.

### Cohort-bound contributor drill-down

Performance groups and overall measures now expose ranked complete-history contributors through account/customer read APIs and dashboard links. The stores share cohort reads and interval calculations with summaries. Exact selectors preserve state contract versions and blocker Operation/code/version/owner identities, including an explicit unassigned group. Contributor statistics use the same per-instance values as the summary, with no history-quality resampling or group truncation before selection.

Summaries emit an opaque selector-bound token containing evaluation time and a fingerprint of both retained sampled cohorts, matching counts, history and verification findings. Token-bound reads exclude evidence published after evaluation and use current retention eligibility. Changed cohorts return an explicit refresh conflict; the dashboard offers a fresh summary. This verifies available evidence rather than persisting a snapshot or reserving workflow state. Contributor responses expose current blockers and bounded verification previews alongside historical durations, with customer tenant identifiers removed. Staleness and deadlines are evaluated at the same observation time.

### Observed state SLA budgets

Pinned workflow declarations now support optional per-state SLA budgets for declared nonterminal states, bounded in the central limits table and normalized consistently across steps. Manifest durations project to whole-second API maps. Current SLA clocks use contiguous retained state visits, preserving entry through metadata updates; gaps, duplicates, invalid event ordering or policy/version changes produce unknown status rather than invented deadlines. Breaches are observational at the budget boundary and terminal states have no active SLA.

Account/customer attention queues and their summaries project the same visit semantics in memory and SQL, with a `sla_breached` filter and breached/unknown counters. Selected workflow and performance contributor views show entry/due times and remaining/exceeded time. Performance cohorts evaluate closed and ongoing visits only with complete workflow history, preserving historical breaches after completion and explicit unknown coverage. Budget changes remain pinned by immutable definition identity and workflow version. SLA attention does not block admission or readiness and does not initiate application actions.

### Optional SLA warning thresholds

Pin `state_sla_warning_percent` alongside state budgets in workflow contracts. Values are whole percentages 1–99 and require a budgeted nonterminal state; declarations agree across steps. Known current visits become `at_risk` at the exact warning time and `breached` at the due time. Warning time uses the same observed visit entry as the budget, preserving metadata updates and unknown-history coverage. Policy drift during a visit makes current SLA history unknown. Thresholds are optional and version changes preserve old pinned observations.

Risk participates in the default attention queue and the `sla_at_risk` filter, with a separate matching-workflow count. Existing filters and frozen cursor evaluation time apply. Selected views and contributors expose warning policy/time, while historical breach metrics retain their meaning. This remains observational and adds no actions, notifications or readiness enforcement.
