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

### Transition readiness guidance

The dashboard and CLI explain the existing versioned transition rules using a
shared Go projection, `OperationWorkflowTransitionReadiness.Guidance`. The
projection identifies reported blockers and their owners/next actions, missing
prerequisite links, unmet prerequisite instances and required outcomes, and the
milestones, policy decisions, passing invariants, and confirmed effects that must
accompany a proposed transition. Unknown future reason codes remain visible.
The Go SDK exposes the same helper; the wire readiness contract is unchanged.

An overview supplies no planned evidence. Its missing milestone or evidence
finding therefore means that evidence must accompany the proposed transition,
not that the business event never occurred. Readiness remains observational:
even a ready result requires the application to check its business row and
authorization before committing. Contract/revision mismatches require a refreshed
snapshot rather than automatic retries or state changes.

### Retained milestone prerequisites

An edge may pin up to 16 `required_retained_milestones` operation/milestone pairs.
Source manifests use `requires_retained_milestones`, validate each pair against a
transaction-backed workflow step, and require an explicit target operation.
Canonical ordering and duplicate rejection participate in immutable contract
identity; applications raise the workflow version when requirements change.

Readiness projects exact retained evidence in the same account/app/tenant/scope,
subject, workflow instance, contract version, source operation and milestone.
Memory and Postgres use the same publication-time ordering and retention clock;
Postgres batches at most 32 distinct pairs in one SQLC query. Account views without
an explicit or reported-state tenant cannot combine another tenant's proof.
Only declared matching facts published by the evaluation clock count, independent
of milestone/history pagination. Results are `verified` with an evidence reference
or `awaiting_verification`, which denies readiness through
`retained_milestone_required`. Planned evidence never satisfies these rules.

Verified means retained application-reported evidence, not external provider
attestation. Missing facts do not prove nonoccurrence; expired or different-version
facts remain unverified. The API, action preview, dashboard, CLI and SDK guards
surface the same result. Application reporting remains authoritative and no
transition, notification, or lifecycle action is executed by this projection.

### Retained prerequisite invalidation

Pinned edges may declare up to 16 canonical
`retained_milestone_invalidations` prerequisite/source pairs. Both selectors bind
to transaction-backed workflow steps; the prerequisite must already be required
on that edge, sources must differ, and duplicate rules are rejected. Invalidation
sources count toward the existing 32-source lookup bound. Rules participate in
immutable contract identity and require a new workflow version when changed.

The existing scoped memory/SQLC lookup projects latest publication and latest
eligible occurrence independently for each source, using the same retention and
evaluation clock. Prerequisites without rules preserve publication order. Ruled
prerequisites use occurrence order, exclude future occurrences, and become
`invalidated` when any configured source occurred at or after the latest eligible
prerequisite. Equal occurrence times invalidate conservatively; publication and
identities only break ties within a source. Late publication cannot undo a newer
business invalidation, while a later prerequisite fact can satisfy the rule again.
The latest applicable invalidator is returned with both evidence references and
`retained_milestone_invalidated` denies readiness. Missing prerequisite evidence
remains `awaiting_verification`, even when an invalidator is retained.

All matching boundaries and contract-version rules from retained prerequisites
apply to both sources. Facts follow normal retention and invalidation is derived,
not permanently latched. The API, preview, dashboard, CLI, and SDKs expose the same
result. No application state, milestone, or VM lifecycle state is changed.

### Retained prerequisite freshness

Pinned edges may attach one maximum-age rule per declared retained prerequisite,
expressed as a whole-second manifest duration and resolved as `max_age_seconds`.
Canonical rules participate in immutable contract identity; changes require a new
workflow version. Bounds live in `pkg/api/limits.go`: 16 rules per edge and at most
315360000 seconds (10 years). No new source selectors or migrations are needed.

Both stores project one evaluation clock with the existing scoped evidence lookup.
Freshness selects the latest nonfuture occurrence and expires inclusively at its
occurrence time plus maximum age. Results distinguish missing, expired, and
invalidated evidence; invalidation wins if both policies fail. Policy, evaluation
time, expiry, age, and remaining duration explain renewal through the API, preview,
dashboard, CLI, and SDKs. Missing facts omit derived expiry. SDK guards require
usable evidence and matching freshness policy details.

Renewal is an application-owned business operation whose transaction publishes a
new fact. Gregale provides point-in-time readiness, grants no lease, and does not
extend retention or change business state. Applications still lock and verify
business rows and authorization before committing.

### Retained prerequisite recovery plans

Missing, expired, and invalidated readiness results include an optional `recovery`
object derived from the pinned prerequisite selector. It identifies `reason`,
`operation`, `milestone`, and three ordered steps: `perform_operation`,
`publish_milestone`, then `recheck_readiness`. Verified evidence omits recovery.
For invalidation, `after_occurred_at` is the exclusive invalidator occurrence
threshold; for expiry it is the prior prerequisite occurrence. Any freshness
limit must still be satisfied when readiness is checked again.

The application authorizes and performs the actual business action, commits its
milestone with the true occurrence time, and publishes it before rechecking the
original transition. The new fact must match the existing ownership, subject,
workflow instance, and contract version. Never backdate or fabricate a fact to
satisfy a recovery plan. Missing retained evidence does not prove the business
action never happened: inspect authoritative rows before retrying an action.

Plans are bounded by the existing 16 retained prerequisites per edge, with exactly
three steps per result. They do not recursively resolve producer dependencies or
assert that a producer can run from the current state. The application must check
that operation's own requirements. API previews and overviews return these same
plans; shared dashboard/CLI guidance and Go, Node, and Python SDK types expose
them. No new lookup, manifest rule, migration, or execution permission is added.

### Alternative retained prerequisite groups

Pinned edges may declare up to 8 named alternative groups, each containing 2–16
unique retained milestone selectors. Any verified selector satisfies its group;
all groups and mandatory requirements must pass. Canonical IDs and selectors
participate in immutable contract identity and require a new workflow version
when changed. Bounds live in `pkg/api/limits.go`, and alternatives count toward
the existing 32-distinct-source lookup bound across the workflow contract.

Freshness and invalidation rules may target mandatory or alternative selectors.
The existing scoped lookup and evaluator preserve evidence and recovery findings
for every option. Only an entirely unsatisfied group denies readiness through
`retained_milestone_group_unmet`; failed unchosen alternatives are diagnostic.
Shared guidance distinguishes choices from cumulative recovery requirements.
SDK guards require complete matching groups and at least one usable proof with
its pinned freshness policy, rather than trusting the satisfaction boolean alone.

This extension supports alternatives between individual retained facts, without
nested Boolean expressions, new queries, or migrations. Gregale evaluates options
and does not choose or execute a business path. Applications keep authorization
and business-row checks; readiness remains a point-in-time observation.

### Compound retained prerequisite paths

An alternative group may declare uniquely named `paths` with nonempty `all_of`
facts instead of a flat `any_of` list. Every fact in one path must be verified;
passing facts from different paths cannot combine. Every group and mandatory
prerequisite still applies. Existing flat contracts retain their serialized shape.

Canonical path IDs and selectors participate in immutable contract identity and
require a new workflow version when changed. A group accepts 2–8 paths, with at
most 16 total fact occurrences including shared selectors repeated across paths.
Limits live in `pkg/api/limits.go`. This preserves expanded-evidence bounds, while
the existing 32-distinct-source lookup covers every path and invalidation source.
Cloning copies both paths and their fact slices to preserve pinned definitions.

The same evaluator and clock apply freshness, invalidation, and recovery rules to
every fact. Group results contain all path findings, with satisfaction derived
from complete paths. Shared guidance names valid paths or describes recovery per
blocked path. SDK guards verify complete matching path evidence and consistent
satisfaction flags. No query or schema migration is introduced. Applications
choose, authorize, and execute business paths; readiness grants no execution lease.

### Application-reported path selection

Readiness requests and business transition reports may carry at most 8 canonical
`group_id`/`path_id` selections, one per compound group. Choices bind to the pinned
edge and version. Read-only checks without choices continue to expose available
paths; checks with choices require the chosen paths rather than any valid sibling.
Unknown choices deny readiness. Metadata-only reports and initial snapshots cannot
carry choices. Direct legacy reporting can omit them without inferring a path.

Guarded transitions require choices for every compound group, verify complete
selected-path evidence and response identity, and queue those exact choices.
Requests and selections are copied before calling an external checker. Publication
verifies choices against receipts before acknowledging application outbox rows.

An additive platform report-ledger migration and an additive customer outbox
column retain selections with the business transaction. Choices participate in
canonical report identity, preserving old fingerprints when omitted. Memory and
SQLC projections expose them in receipts and transition history; dashboard/CLI
views display the application's choice. The application owns selection, execution,
and authorization, and readiness remains a point-in-time observation.

### Decision reasons for selected paths

Selections may carry an optional business decision milestone ID and a bounded
snapshot of its code, description, rule ID, and version. All five fields are
supplied together and participate in canonical report identity. The reference
must be an evidence milestone from the same application transaction and Operation;
validation and publication compare the snapshot against the typed decision payload
and its declared workflow instance and pinned contract version. Invalid, missing,
ordinary, or mismatched facts cannot substantiate a path explanation.

SDK transaction helpers build references from exactly one queued decision milestone.
String-only selection values preserve defensive value copies and receipt equality.
Existing JSON columns retain explanations through history and receipt replay; no
additional migration is needed. Dashboard and CLI display the validated snapshot.
Readiness checks validate shape, while report validation verifies evidence. A
recorded explanation describes an application decision and grants no authority.

### Pinned decision requirements for named paths

A named retained prerequisite path may declare one optional existing policy
requirement (milestone, code, rule ID, and rule version). Contract compilation
validates bounded fields and requires the milestone in the same declared workflow
version. Canonicalization and projection copy the optional requirement; immutable
edge comparison already includes path contents. Omitted requirements preserve
legacy serialization and behavior.

Readiness evaluates planned decisions per path alongside retained facts. Findings
retain the exact expected requirement and optional reported decision, and derive
satisfaction from both. Group selection cannot substitute another path. Guidance
explains expected and reported rules. SDK guards independently compare diagnostics
against supplied planned facts, attach the actual staged decision reference, and
reject mismatched supplied references. Callback inputs copy decision values and
milestone lists before checks.

Precommit and publication require an explicit choice in a group that opts into
path decision rules, and matching linked typed evidence when the chosen path is
constrained. Existing Operation ownership, workflow instance, and pinned version
validation apply to that reference. An unconstrained sibling remains available.
This adds no database column, remote business action, or execution authority.

### Retained revision comparisons

Business workflow history derives a comparison for each returned revision against
the preceding report for the same tenant and workflow run on that page. Both stores
use one pure projection after scoped history selection. Consecutive unique revisions
with the same effective pinned version support state, blocker, path, and decision
snapshot comparisons; missing baselines, gaps, duplicate revisions visible on the
page, and version changes are explicit statuses. No additional SQL or hidden cursor
fetch is introduced. At most 64 changes and 64 deduplicated evidence references can
be produced from the existing bounded report fields.

Comparison evidence links the retained reports' published milestone and decision
identities and explicitly labels expected verification references. It does not
claim causal attribution, external verification, or business resolution. Per-edge
path omission is not a path removal, especially on metadata updates. Comparisons
are derived only for responses and never mutate memory receipts or report identity.
Dashboard, CLI, and SDK models expose the same bounded result. This changes no
business execution ownership, database schema, or application transaction behavior.

### Application-reported reasons for workflow changes

A workflow report with a source state may carry one optional change reason linking
a typed business decision fact and a bounded value snapshot of its payload.
Existing transaction-backed milestone validation enforces the same Operation,
workflow instance, and pinned contract version, and compares all decision fields.
Initial snapshots cannot carry reasons. Metadata-only updates may carry exactly
that reason as their sole evidence, preserving the prohibition on unrelated
metadata evidence. The application owns this explanation and all business actions.

An additive platform ledger column and additive customer outbox column store the
reason as object-or-null JSON. Nil omission preserves old canonical fingerprints.
SDK helpers attach exactly one queued decision to the last workflow update;
publishers validate, preserve, and match reasons against receipts before
acknowledgment. Copies isolate stored memory records and response projections.

History and latest state expose the reason, and revision comparisons include it
even without a baseline. Comparison evidence identifies the decision when facts
are compared; reasons describe application reasoning rather than inferred cause.
Dashboard and CLI render that distinction. This introduces no execution authority,
remote application action, or permanent evidence retention.

### Retained workflow outcome paths

History reports expose `outcome_link`, correlating each report (including its
application decision reason and selected paths) with the first reported outcome
at or after it on the same retained page. The link includes the outcome report,
Operation, revision and code. This is an observed sequence, not proof that a
rule caused an outcome. An outcome on the report itself has zero duration.

Correlation requires a unique consecutive revision path within the same tenant,
workflow instance and pinned contract, consistent state continuity, and
nondecreasing application occurrence times. Gaps, duplicate revisions, contract
changes and inconsistent timing are explicit unavailable statuses. The extra
pagination lookahead is excluded from correlation; clients must not interpret
an unavailable outcome as an ongoing workflow. Terminal states without a
reported outcome code cannot produce an outcome link.

Elapsed seconds cover only this report-to-outcome path, not total workflow
completion time. Blocked seconds count intervals whose preceding snapshot had
at least one blocker, counting overlapping blockers once. These durations are
meaningful only for `linked` status. API, dashboard and CLI expose the same
projection; no new persisted data or migration is required.
