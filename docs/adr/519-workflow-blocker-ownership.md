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

### Business decision outcome summaries

The existing workflow performance summary now includes `decision_outcomes`,
also shown on the workflow performance dashboard. Go, Node and Python clients
receive the same additive projection without a new endpoint or migration.
It uses the existing latest retained completed cohort (up to 100 workflows),
account/app/environment/customer boundaries, per-instance 1024-report history
window, and complete-history eligibility rules. Ongoing workflows remain in
the separate performance cohort and do not contribute to outcome groups.

A group identifies the pinned contract, decision code, rule ID/version and
latest application-reported outcome. Path groups additionally identify the
selected prerequisite group and path; choices without a linked decision still
form path groups. Both application change reasons and selected-path decision
snapshots classify workflows. Each workflow contributes once per exact group,
even when it repeats that decision or reports it through both sources. Only
reports at or before the first terminal report classify the completed path.
Groups overlap; summing their counts does not produce a cohort total.

Completion and blocked-time distributions reuse the full completed workflow's
observed state and blocked durations, with nearest-rank p50/p95 values. They
are not decision execution times or decision-to-outcome latency. Overlapping
blockers count once per workflow. The latest reported outcome is an application
claim, and an association does not prove that a business rule caused it.

Coverage separates sampled workflows into incomplete histories, missing
reported outcomes, unclassified complete workflows, and classified workflows.
Cohort truncation distinguishes all matching completed instances from the
bounded sample. At most 64 groups are returned, in canonical identity order
(contract, kind, decision code, rule ID, rule version, group, path, outcome).
`groups_truncated` makes omissions explicit; classified workflow counts still
include those whose groups were omitted. Group accumulation retains only the
64 smallest identities, with one duration sample per workflow/group, bounding
memory without losing counts for any returned group.

### Contributors to business decision outcome groups

The existing workflow performance contributor endpoint now accepts
`dimension=decision_completion` or `dimension=decision_blocked`. Both require
`cohort=completed`, the summary's `cohort_token`, and an exact group returned
by its bounded `decision_outcomes` projection: `kind`, `contract_version`,
`outcome_code`, and the decision code/rule ID/rule version triple. Decision
kind requires that triple and rejects path selectors. Path kind additionally
requires `group_id` and `path_id`; its decision triple is entirely present or
entirely absent, matching the summary. Legacy state/blocker/owner selectors
are rejected for these dimensions, and decision selectors are rejected for
legacy dimensions. Invalid or omitted summary groups return invalid_argument.

Summary and contributor reads share classification: complete retained
histories, latest reported outcome, and facts at or before the first terminal
report. Repeated identities contribute once per workflow/group. The existing
cohort fingerprint verifies evaluation time, both sampled cohorts, and their
retained evidence. A changed or expired cohort returns the existing 409 and
requires a fresh summary. Tokens cannot be borrowed across owners or workflow
selectors. No new query or migration is required.

Results include all eligible contributors (up to 100), ranked by the selected
full-workflow duration descending with stable identity ties. The existing
`duration` distribution matches the summary group's completion or blocked
statistics. `decision_outcomes` preserves the summary's coverage and group
truncation alongside the existing cohort coverage. Each item optionally
includes `decision_evidence`: the earliest qualifying report/Operation/revision,
its state transition and occurrence time, and the validated decision fact ID
and public snapshot where present. A path without a decision is evidenced by
its report. Current state retains the latest reported outcome and report IDs.
These are application claims and sequence associations, not causal proof.

Dashboard summary duration cells open the corresponding contributors using
the cohort token. Contributors link to the qualifying Operation, latest
reporting Operation, and retained workflow history (transitions, blockers and
outcome). The existing refresh prompt handles cohort changes. Go and Node
helpers serialize the exact selectors; generated Node and Python clients
expose the additive parameters and evidence DTOs.

### Compare observed rule versions

`decision_outcomes.rule_comparisons` adds side-by-side version statistics to
the existing performance summary and decision contributor coverage. Identity
is the pinned workflow contract, decision code and rule ID; versions are opaque
strings displayed in lexical order. No newer/older relationship is inferred.
The dashboard shows outcome counts and percentages and full-workflow completion
and blocked-time p50/p95 distributions for every returned version.

Coverage uses the same bounded completed sample and latest retained 1024 reports
per workflow, excluding pagination lookahead. A workflow counts once per exact
observed identity/version, whether its decision was reported as a change reason,
selected-path fact, or both. Membership stops at the first retained terminal
report. Incomplete histories can identify observed versions only; they never
supply duration or outcome statistics. Missing history may hide other versions
or an earlier terminal report, so these coverage memberships do not establish
the full business path. Versions without eligible observations display no data.

For each version, observed counts partition into eligible workflows, excluded
incomplete histories, and complete workflows without a latest reported outcome.
Unattributed workflows have no observed decision identity (including paths with
no decision), which does not prove a rule was unused. A workflow can report
several versions and appear in several columns. Cohort coverage and truncation
remain on the enclosing decision summary; these are observed associations,
not a controlled experiment, significance test or proof of rule causation.
`compared` means at least two returned versions have eligible observations;
otherwise status is `insufficient_data`.

Each outcome's integer proportion uses all eligible workflows for that version:
`floor(workflow_count * 10000 / eligible_workflow_count)` basis points. The
dashboard displays percentages to two decimal places. Proportions may sum below
100% because of rounding or omitted outcomes. Up to 16 comparison identities,
16 versions per identity, and 16 outcome codes per version are retained in
canonical/lexical order, with independent truncation flags. Accumulators retain
only the smallest keys, preserving every sample for returned identities and
durations/denominators across omitted outcomes. Comparisons do not depend on
which decision outcome groups survive the separate 64-group limit.

Go, Node and Python expose the additive DTOs. Existing cohort tokens and
contributor refresh behavior remain in force. No endpoint or migration is added.

### Separate observed rule version overlap

Rule comparisons now count distinct sampled workflows reporting multiple versions
for the same contract/decision code/rule identity. The observed overlap count
includes incomplete histories; the eligible overlap count requires complete
history and a latest reported outcome. Version membership is collected before
comparison/version display limits, so an omitted version cannot create a false
single-version classification. Repeated facts through change reasons and selected
paths still count once per workflow/version. Decisions after the first retained
terminal report remain outside classification.

Each returned version has a `single_version` cohort: complete completed histories
with a reported outcome and exactly one observed version for this identity.
It includes counts, full-workflow completion and blocked-time distributions,
outcome counts/proportions, and outcome truncation. `single_version_status`
requires eligible single-version workflows in at least two returned version
columns to report compared. Empty subsets provide zero counts, empty outcomes,
and the existing zero-duration convention. Incomplete histories never qualify;
absence of another version in partial history is not sufficient. This restriction
refers to reported decision reasons and selected-path facts, not every rule that
might have executed in the application. It does not establish causality.

The dashboard offers a separate single-version comparison and a bounded list of
overlapping runs. Up to eight examples per comparison appear in stable workflow
identity order, with up to 16 earliest-per-version decision reports in revision
order and explicit truncation flags. Counts include omitted examples/evidence.
Examples carry subject, instance, latest Operation, history/eligibility flags, and
public decision report/fact snapshots. Account views link to the scoped retained
workflow history and reporting Operations; customer responses omit tenant IDs.
Tied revisions and lexical version names do not imply a policy replacement or
an ordering between decisions. History links show their current retained view.

Go, Node and Python expose these additive fields and DTOs through the existing
summary and contributor APIs. Existing owner boundaries, cohort tokens, refresh
behavior, sample limits, and version/outcome truncation rules remain in force.
No endpoint or migration is added.


### Application-reported decision conditions

A workflow milestone step may declare `decision_conditions`, a list of unique
condition IDs and public descriptions pinned to that workflow contract version.
Typed `gregale.business-decision.v1` decisions may optionally report `conditions`
with the same ID and exact description and a `pass`, `fail`, or `unknown` result.
Each declaration and report is bounded to 16 conditions; IDs are lowercase slugs
of at most 64 bytes and descriptions are nonempty UTF-8 text of at most 1024 bytes
without control characters. Results are optional; omission means unreported,
not passing, failing, or unknown. They do not become transition prerequisites.

Milestone publication validates each reported condition against the pinned step.
Change reasons and selected-path snapshots retain the complete condition evidence
and require equality with the referenced transaction-backed fact. SDK receipt
validation includes these snapshots before acknowledging the outbox. Go copies
nested condition slices at mutable boundaries; SDK canonical snapshots sort by
condition ID. Existing declarations and decisions omit these optional fields,
keeping their serialized shape and retained reports compatible. The existing JSON
payload and snapshot columns carry the data; no migration or new endpoint is needed.

History, decision facts, revision decision snapshots, and contributor views show
the application-reported results. Rule cohort identity and outcome grouping remain
contract/version/decision/rule/path based, so conditions do not split a cohort or
imply causation. Applications own predicate evaluation, authorization, and actions.
Public explanations must not contain private input values or secrets.

### Compare reported conditions between retained revisions

Revision comparisons expose `condition_comparison`, and decision contributor
`decision_evidence` carries the same comparison for its qualifying report. The
baseline must be retained, uniquely numbered, consecutive, in the same tenant
and workflow run, and on the same effective pinned contract version. Missing
baselines, revision gaps, ambiguous revisions, and changed contracts retain the
existing comparison status and produce no inferred condition changes. Contributor
comparisons use the same latest bounded history slice as decision membership,
excluding the database lookahead; they do not fetch a wider history or alter
cohort fingerprints, eligibility, ranking, or grouping.

Conditions are matched by ID within the application reason or a selected path
group. Only explicit current decisions participate. A metadata report without a
reason or path decision does not clear an earlier decision. Within two explicit
decisions, an added ID is `newly_reported`, an absent current ID is `unreported`,
and a changed result or description is `changed`. Explicit `unknown` remains a
reported result, distinct from absence. Matching IDs across different reported
rule versions does not infer predicate equivalence or rule execution; both rule
identities and public descriptions remain visible. Unchanged conditions are omitted.

The projection preserves before/after condition snapshots and the exact report,
revision, Operation, decision fact, code, rule identity, and selected path on each
available side. When the preceding report lacks a decision in that context, no
previous fact reference is invented. Contributor comparisons describe the entire
qualifying report, including other reason/path contexts, rather than claiming the
changes explain the selected outcome group.

At most 64 changes are retained, ordered by reason first, then lexical path group,
then lexical condition ID. A separate truncation flag discloses omitted changes.
Structured condition changes replace embedding condition arrays in the generic
string decision snapshot, keeping those snapshot strings within their existing
bounds. Existing report payloads and persistence are unchanged; optional derived
fields need no migration or new endpoint. Dashboard history, contributor views,
SDK DTOs, and CLI output expose the comparison and references. Facts remain
application-reported evidence and do not establish why a decision or outcome occurred.

### Condition-based outcomes and contributors

The performance summary optionally exposes `condition_outcomes` for its existing
completed cohort. It associates explicitly reported condition results from
application reasons and selected-path decision facts at or before the first
terminal report with the latest reported workflow outcome. Only complete retained
histories with a reported outcome qualify. Existing decision/path summaries,
cohort selection, tokens, ownership, and rule comparisons are unchanged.

An exact condition identity includes effective contract version, decision code,
rule ID/version, condition ID, and its public description. Including description
keeps distinct declared meanings separate even when IDs are reused by different
milestone steps. The result and reported outcome complete the group identity.
Repeated reports and reason/path references count once per workflow/result group.
Missing conditions are unreported, never implicitly `unknown`. A workflow that
reported several distinct results for one exact identity contributes to each
observed result group. Group counts overlap and must not be summed.
`changing_result_workflow_count` counts these contributors per group, and counts
linked workflows once across all identities in overall coverage. A change across
rule versions or descriptions creates separate identities and is not implicitly
classified as a changing result for one predicate.

Coverage partitions sampled workflows into incomplete history, missing outcome,
excluded condition evidence, no reported conditions, and linked evidence. Linked
and changing-result coverage includes identities omitted by the group display
limit. The latest 100 completed workflows are sampled as before. A workflow with
more than 256 distinct condition identities before the first terminal report is
excluded entirely from condition groups and contributors, with an explicit
coverage count. This prevents partial evidence from presenting a changing-result
workflow as a single-result workflow. The identity limit is checked before the
independent group display cap. Returned groups retain the lexically smallest 64
contract/decision/rule/version/condition ID/description/result/outcome keys;
monotonically decreasing retention ensures every returned group includes every
eligible sampled contributor. All limits live in `pkg/api/limits.go`.

Existing contributor routes add `condition_completion` and `condition_blocked`.
They require completed cohort, nonempty cohort token, and the exact returned
condition group via contract version, decision/rule triple, condition ID,
description, result, and outcome. They reject unrelated kind/path/state/blocker/
owner selectors; other dimensions reject condition selectors. Omitted groups
cannot be queried. Existing changed-cohort handling requires a refreshed token.

The summary and drilldown use the same condition membership calculation. Each
contributor carries its earliest qualifying decision fact, the selected condition
snapshot, all observed results for that exact identity, and the existing revision
condition comparison. Ranking remains by full completed workflow duration or
blocked duration with stable identity ties; these are not durations spent under
that condition. Shared DTOs, Go/Node/Python clients, and dashboard links expose
this evidence through existing ownership boundaries. No SQL, persistence,
migration, predicate evaluation, action authority, or causal inference is added.

### Active workflow condition visibility

Performance summaries expose optional `active_conditions` for the same latest
100 ongoing workflows and evaluation time. Complete retained histories supply
the latest explicit reason decision and latest nonempty selected-path snapshot.
Each explicit decision replaces its condition array, so an omitted condition is
unreported rather than carried forward as unknown. Metadata-only reports retain
prior evidence. A new contract version resets contexts; paths without a decision
fact remove their context. Different rule versions in independent contexts stay
separate; conflicting results for the same exact identity exclude the whole run.

Coverage separates incomplete, conflicting, bounded-evidence and unclassified
runs. Limits are 256 contexts and 256 final identities per workflow; the first 64
lexical groups retain exact memberships. Counts overlap across conditions. Each
group shows full workflow elapsed/blocked duration, evidence age and stale-state
counts. State staleness uses the declared workflow threshold, not predicate
expiry. These snapshots are application reports, never current business truth.

`active_condition_elapsed` and `active_condition_blocked` contributors require
`cohort=ongoing`, the summary token, and the exact returned contract/decision/rule/
version/condition ID/description/result identity. Outcome, path and other group
selectors are rejected. Shared selection logic gives matching counts, ranked
workflow links, decision fact/report timestamps and evidence age frozen at the
summary evaluation time. Changed retained evidence requires a refreshed summary.
Completed condition outcome behavior remains unchanged. No migration or business
execution is introduced; applications own evaluation and actions.

### Pinned condition report freshness budgets

A condition definition may declare `evidence_max_age_seconds`, a nonnegative
integer bounded to ten years. Omission or zero leaves the budget unconfigured.
For example, a milestone step can declare:

```yaml
decision_conditions:
  - id: payment-verification
    description: Payment verification reported by the application
    evidence_max_age_seconds: 900
```

The active condition view resolves the selected fact's milestone against the
reporting Operation's immutable definition and exact workflow contract version.
It never reads the current deployment to reinterpret historical evidence. The
source definitions participate in the cohort fingerprint. Read queries project
this metadata from existing definition JSON and milestone rows; no migration is
required. Memory and PostgreSQL stores use the same fact ownership and milestone
mapping. Budgets belong to definitions, not application-reported result payloads.

Each returned active group partitions its workflow count into
`within_budget_workflow_count`, `overdue_workflow_count`, and
`no_budget_workflow_count`. Contributor evidence includes `condition_freshness`
with status, configured budget, refresh deadline, and optional overdue seconds.
A report at 12:00 with a 900-second budget is within budget before 12:15 and
becomes overdue at 12:15. All calculations use the token-bound `evaluated_at`.
The optional `condition_evidence_status` selector filters active contributor
queries by `within_budget`, `overdue`, or `no_budget`; a status must have matching
contributors in the returned group. Other dimensions reject this selector.
Dashboard overdue links rank workflows by their full observed blocked duration.

Metadata-only reports preserve evidence and its original clock. An explicitly
republished decision snapshot refreshes reporting age; it does not prove the
application performed a new verification. Unknown remains an explicit business
result and is independent of reporting freshness. Unreported conditions and
incomplete histories retain their existing coverage handling. Workflow state
staleness is separate. No facts expire and no application actions are triggered.

### Condition evidence refresh ownership and guidance

Pinned condition definitions may include independent optional `refresh_owner`
(up to 128 UTF-8 bytes) and `refresh_instructions` (up to 1024 UTF-8 bytes).
Both are public text without controls or surrounding whitespace. Omission leaves
the owner unassigned or instructions unavailable; neither field configures a
freshness budget or triggers an action. For example:

```yaml
decision_conditions:
  - id: payment-verification
    description: Payment verification reported by the application
    evidence_max_age_seconds: 900
    refresh_owner: payments-team
    refresh_instructions: Recheck the payment provider and publish an updated decision snapshot.
```

Contributor `condition_freshness` carries the selected fact's pinned owner and
instructions, including overdue evidence. The source definition belongs to the
reporting Operation and milestone; current deployment changes do not reinterpret
retained reports. Existing definition JSON, metadata projections and cohort
fingerprints carry these fields, so no SQL schema migration is needed.

Active summaries also expose `refresh_owners`, grouping eligible ongoing
workflows with overdue selected condition reports. A workflow contributes once
per owner; multiple overdue conditions for that owner increase
`overdue_condition_count` without multiplying full-workflow durations. Workflow
memberships overlap across owners. Missing owners use the explicit unassigned
group. `overdue_workflow_count` and `overdue_condition_count` remain exact across
the eligible sample before display limits. The first 64 lexical owner strings
are displayed (unassigned first), with `refresh_owners_truncated` when limited.
These counts are independent of condition-group display truncation and inherit
incomplete-history, conflicting-snapshot and evidence-limit exclusions.

`dimension=condition_refresh_owner` requires `cohort=ongoing`, the summary token,
and exactly one of `owner` or `unassigned=true` matching a returned owner group.
Contract, state, blocker, decision, path, outcome and condition/status selectors
are rejected. Contributors rank by full observed blocked duration with stable
workflow ties, rather than inferred time under an owner. Each carries the first
16 overdue condition facts in canonical identity order as `refresh_evidence`,
with exact `refresh_evidence_count` and `refresh_evidence_truncated`. Fact links,
report timestamps, refresh deadlines and per-condition guidance remain visible;
a workflow with many conditions is still counted once. Dashboard owner links
open this same bound contributor view.

Refresh instructions are displayed as text and never executed. Applications own
verification, authorization and actions. Reporting freshness, condition results,
and workflow state staleness remain separate observations.

### Workflow refresh prioritization

The ongoing performance contributor view adds overall
`condition_refresh_age` and `condition_refresh_blocked` dimensions. Both require
`cohort=ongoing` and the summary token, reject all condition/decision/state/
blocker/owner selectors, and include eligible workflows with at least one
selected overdue condition report. Each workflow appears once across all teams.
These views use the same bounded latest-100 sample, complete-history requirement,
pinned definitions, conflict exclusions and evaluation time as active coverage.
Condition and owner display caps do not hide qualifying workflows. A fresh
summary with zero overdue workflows produces an empty ranked list.

Age priority ranks by the age in whole seconds of the oldest selected overdue
report, not seconds past its refresh deadline. Blocked priority ranks by full
observed workflow blocked duration, not duration attributed to a condition or
team. Stable workflow identity breaks equal-score ties. Response `duration`
statistics and contributor `observed_seconds` use the selected ranking measure.
A change in retained evidence requires refreshing the bound summary.

Each contributor includes `refresh_priority`, with the oldest overdue report's
full qualifying evidence and pinned deadline/guidance, its age, the full blocked
duration, all distinct assigned refresh owners in lexical order, an exact
unassigned condition count, and a plain explanation of the ranking. Timestamp
ties for the oldest evidence use canonical condition identity. Assigned owner
lists are bounded by the existing 256-condition identity eligibility limit;
they remain complete even when the 16-fact canonical evidence preview is
truncated. The oldest ranking fact is included independently of that preview.
`refresh_evidence_count` counts all overdue conditions in these workflow views;
`refresh_evidence_truncated` explicitly flags omitted preview facts. Existing
owner contributor views continue to count and preview only their selected owner.

Dashboard summary links open either priority view. Each ranked workflow shows
all teams, report timestamps, refresh deadlines, full blocked duration,
application-declared guidance and links to the reporting Operations and workflow
history. These observations do not assign business severity, infer causation,
expire facts, or trigger verification or actions. Applications keep execution
and authorization. Existing definition/history metadata is sufficient; no
migration or new persistence is introduced.


### Condition refresh progress

Performance summaries optionally accept `baseline_token` from a previously saved
summary. The token binds the query and evaluation time; comparison deliberately
accepts changed evidence fingerprints. It does not store historical cohort
membership. Progress reconstructs evidence visible at the baseline from retained
report publication timestamps for today's bounded latest-100 completed and
latest-100 ongoing workflow samples. Workflows absent from today's sample are
not treated as cleared backlog.

Coverage explicitly counts compared workflows, workflows without a baseline or
baseline overdue conditions, and exclusions for incomplete history, ambiguous
condition results or evidence limits. Missing publication timestamps and gaps
exclude comparison. Contract boundaries are permitted for progress classification
without relaxing existing duration or active-condition eligibility.

Exact condition and decision/rule identities compare baseline overdue facts with
current explicit contexts. Results may change. Statuses are `refreshed` (newer
report within its pinned budget), `still_overdue` (including newer reports already
overdue), `unreported`, `contract_changed`, and `budget_removed`. Cross-contract
facts are not compared. Newer-report counts and all status totals precede the
16-change preview cap. Workflow completion, missing conditions and removed
budgets do not establish successful business verification.

Dashboard bookmark links preserve the baseline and show coverage, before/after
results, reporting timestamps, pinned deadlines and guidance, reporting Operation
links and workflow history. Customer projections omit tenant identifiers. Go,
Node and Python SDKs expose the optional summary comparison. No new persistence,
migration, automatic verification or application action is introduced.


### Expected condition reporting coverage

A pinned workflow step condition may declare `expected_states`, a canonical
unique list of up to 32 declared workflow state names. Omission makes no
expectation. YAML/TOML manifests and API definitions use the same field. This
annotation is observational: it never gates transitions, invokes checks or
changes application-reported pass/fail/unknown results.

Performance summaries include `condition_coverage` for the bounded latest-100
ongoing sample. Expectations for the current state come from the latest reporting
Operation's immutable definition and current contract, not a global workflow
registry or the union of every Operation's definitions. Workflows with no declared
expectations are counted as unconfigured, not complete. Continuous retained
history is required; incomplete histories, conflicting latest results and
condition/context evidence limits are explicitly excluded.

Evidence matches the declaring workflow step and exact condition ID and
description in the latest explicit contexts of the current contract. Two steps
with the same condition vocabulary remain separate expectations. Explicit
omission cannot resurrect an older condition, metadata-only reports preserve
evidence, and contract changes reset contexts. Conflicting results for a step
and condition exclude that workflow. At most 256 expected or source step-condition
identities qualify, using the existing condition identity bound.

Coverage counts expected, reported, missing and overdue conditions. Reported
includes overdue, so expected equals reported plus missing; overdue is a subset
of reported. A reported fail or unknown is still evidence. Missing means absent
from current contexts, not a failed check or proof no historical report exists.
Report freshness retains the source Operation's pinned age budget rather than
substituting a newer definition's budget. Counts precede the canonical first-16
condition preview per workflow and an explicit truncation flag. Workflows appear
in stable identity order. Customer projections omit tenant identifiers.

The dashboard shows coverage/exclusions, expected step and milestone vocabulary,
application-declared guidance, matching result and report timestamp, pinned
freshness status, reporting Operation links and workflow history. API schemas
and Go, Node and Python SDKs expose the projection. SQLC reads existing pinned
metadata; no migration, new persistence, authorization or execution is introduced.

Example condition declaration on a workflow step:

```yaml
decision_conditions:
  - id: payment-confirmed
    description: Payment provider confirmation is available
    expected_states: [ready-to-ship]
    evidence_max_age_seconds: 3600
    refresh_owner: payments
```

`ready-to-ship` must also be in that step's workflow `states` list. The application
still evaluates and reports the result through its business decision facts.


### Missing condition report groups and contributors

Condition coverage adds `missing_groups`, ordered by effective contract version,
current state and declared refresh owner (empty owner is `unassigned`). Groups
count all matching missing conditions before condition previews. Each workflow
contributes once per owner to `blocked_time`; a workflow with missing conditions
for several owners appears in several groups. The measure is full observed
workflow blocked duration, not delay caused by a condition or team. Only the
first 64 groups are displayed using the existing active-condition group bound;
`missing_groups_truncated` flags omissions without changing coverage totals.

The `condition_missing_owner` contributor dimension requires `cohort=ongoing`
and `cohort_token`, an exact returned missing group selected by `contract_version`,
current `state` and exactly one of `owner` or `unassigned=true`. It rejects all
condition/result/status, decision/path/outcome and blocker selectors. Invalid or
undisplayed groups are rejected. The existing query-bound token checks both
cohorts' retained evidence before deriving contributors; changed or expired
evidence requires a new summary. Evaluation time remains the summary's time.

Missing contributors share the full coverage eligibility and explicit-context
selection helper: pinned expectations from the latest reporting Operation,
complete histories, conflict and evidence-limit exclusions, exact declaring
step and condition vocabulary, and no inference of failed verification. Display
caps cannot hide matching workflows or owners from contributor counting. Each
workflow appears once in its selected group and ranks by full observed blocked
duration descending, with stable workflow identity ties. `observed_seconds` and
response `duration` statistics use that same measure; no evidence age is invented
for an absent report.

Contributor `missing_condition_count` is exact for the selected state and owner;
`missing_conditions` previews the first 16 matching expected declarations in
step/condition ID order, independent of the overall coverage preview.
`missing_conditions_truncated` flags omissions. Entries have no evidence report
and include pinned vocabulary, step, milestone, budget and public refresh
guidance. Customer responses omit tenant identifiers from contributors and
nested condition coverage. API schemas and Go, Node and Python SDKs expose the
same group and contributor fields.

The dashboard coverage card shows missing-group counts and snapshot-bound links.
Drilldowns show coverage exclusions, exact missing counts, expected declarations
and guidance, the latest reporting Operation and pinned definition revision, and
workflow history. The previously misplaced schema text in the summary template
is replaced by the actual expected-condition coverage card. These observations
neither assign severity nor execute refreshes, business checks or application
actions. No new SQL, migration or persistence is required.


### Missing-report progress from a saved baseline

The existing optional summary `baseline_token` now also returns
`missing_progress`. Its query and evaluation time anchor reconstruction; the
changed evidence fingerprint is intentionally accepted for comparisons. Current
retained latest-100 completed and latest-100 ongoing samples are compared, not
original saved cohort membership or every historical backlog item. Workflows
outside today's sample never count as resolved.

The last report published by baseline time supplies baseline state, contract and
reporting Operation. One bounded, ownership-scoped SQLC batch reads those
Operations' immutable workflow definitions; the memory store uses the same
retained pins. The baseline declaration is never substituted with today's
Operation definition. Missing historical definitions have an explicit exclusion
count. Definition lookup is comparison-only and does not enter the contributor
fingerprint, so snapshot-bound drilldowns still work from comparison summaries.
No schema migration or new persistence is needed.

Continuous baseline report history within one contract is required; today's
history may have contract boundaries solely to classify `contract_changed`.
Unknown publication timestamps, gaps and other report-history failures exclude
comparison. Historical terminal reports produce no baseline missing gaps.
Reporting conflicts and existing expectation/context/identity limits exclude
comparison. Coverage explicitly partitions today's samples into compared,
no-baseline, no-baseline-missing, incomplete, conflicting, over-limit and
unavailable-definition workflows. This reconstructs report history and pinned
expectations, not historical verification obligations or the original queue.

Baseline missing entries retain declaring step, milestone and full condition
definition. Current state expectations are found by step and condition ID:
`received_report` requires identical declaration and milestone plus a current
matching fact; `still_missing` retains the same expectation without evidence;
`expectation_changed` includes changed vocabulary, expected states, budget,
owner or guidance; `not_expected` means the condition left the current state's
expectation set; `contract_changed` never compares facts across contracts.
State changes are explicitly flagged and current terminal reports remain visible
without implying resolution. Current evidence in changed declarations is shown
separately and does not count as filling the old expectation.

Any application result (pass, fail or unknown) counts as received evidence; its
source-pinned freshness may be within budget, overdue or unconfigured. Received
reporting does not mean successful verification. Metadata-only reports preserve
explicit contexts, omitted conditions stay missing, and changing expectations
does not manufacture a report. All five status counts include every baseline
missing condition before the first-16 canonical step/condition preview, with an
explicit truncation flag. Workflow items are ordered by stable identity.

The dashboard's saved comparison link opens overdue and missing-report progress
cards together, showing coverage/exclusions, baseline report publication time and
pinned reporting Operation, current state, changed definitions, results, report
age deadlines, reporting Operation links and workflow history. Customer
projections omit tenant identifiers. API schemas and Go, Node and Python SDKs
expose the optional comparison; execution and verification remain application
responsibilities.


### New and existing missing-report gaps

Saved-baseline performance summaries also expose `new_missing_progress`,
explaining every current missing expectation in comparable ongoing workflows.
The existing `missing_progress` continues to follow baseline missing gaps;
these views overlap and their totals must not be added. They share one
comparison eligibility helper, publication visibility, immutable baseline
Operation definition lookup and full expectation/evidence limits before previews.
No additional SQL, persistence or migration is introduced. Comparisons use
today's retained latest-100 completed and latest-100 ongoing sample, not original
saved cohort membership. Unknown baselines are explicitly counted and never
invented as newly introduced gaps; workflows outside the sample are not resolved.

Current terminal workflows produce no active missing gaps. A terminal baseline
has no active expectations: a later ongoing run's missing declarations count as
`new_expectation`, not lost evidence. Responses expose `baseline_terminal`,
baseline report/state, current state and a state-change flag. Coverage partitions
samples into compared workflows with current gaps, no current active gaps after
comparison, no baseline and existing incomplete/conflicting/over-limit or
unavailable-definition exclusions. No-current-gap counts apply only after valid
comparison, not to excluded or unknown workflows.

For each current missing condition, step and condition ID locate a baseline
candidate. Classification precedence is: `contract_changed` (no cross-contract
Before), `new_expectation` (absent or inactive baseline expectation),
`expectation_changed` (different milestone or full pinned declaration including
vocabulary, expected states, age budget, owner or guidance), `report_lost`
(identical active expectation with prior evidence), and `still_missing`
(identical expectation missing before and now). Changed current states alone do
not create a new expectation if the same declaration was active at both times.
Previous pass, fail, unknown and overdue reports all qualify as prior evidence;
report-age expiry never removes a report and cannot alone create report loss.
Classification describes reporting coverage, not business verification success,
failure, causation or severity.

Before optionally includes the pinned baseline expectation and prior result,
report timestamp, reporting Operation and baseline-evaluated freshness. After
always contains the current missing declaration with no evidence. Status counts
cover all current gaps before the canonical first-16 current step/condition ID
preview and an explicit truncation flag. Workflows appear in stable identity
order; customer projections omit tenant identifiers. The dashboard adds a new
and existing gaps card to the saved comparison view with coverage, before/after
expectations, guidance, prior reporting Operation, baseline/current pin links and
workflow history. API schemas and Go, Node and Python SDKs expose the projection.
Execution and business decisions remain application-owned.

### Reporting-gap category drilldowns

`new_missing_progress.groups` groups comparable current gaps by current contract,
state, refresh owner (including unassigned) and category. Canonical ordering is
contract/state/owner/category, capped at the existing 64-group limit. Counts use
full classified evidence before previews. Each workflow contributes once per
group and can appear in several groups; workflow counts are not additive.

The `condition_gap_owner` contributor dimension requires ongoing cohort, the
current comparison summary's `cohort_token`, saved `baseline_token`, and an exact
returned group selected by contract_version/state/gap_status plus exactly one
of owner or unassigned=true. The baseline must match account/customer, app,
environment and workflow and cannot be later than the current token evaluation.
Changed current evidence returns 409 and requires refreshing the summary. Other
dimensions reject baseline_token and gap_status. Comparison-only historical
definitions use the existing scoped batch SQLC lookup once and do not enter the
current cohort fingerprint.

Contributors include `gap_progress` filtered to the selected category and owner
before the first-16 preview, with exact counts, baseline report/state, prior
evidence and pinned current guidance. Customer responses omit nested tenant
identifiers. Dashboard links carry both tokens and link workflow history and
baseline/current/prior reporting Operations. Lost reports, new expectations,
existing gaps, changed declarations and contract changes remain separate.

Ranks and distributions measure full observed report-only workflow blocked
intervals with stable identity ties, not gap age, causation or business severity.
Intervals spanning contract boundaries are omitted. Current report comparisons permit contract boundaries while baseline history
must be continuous within its contract. Generic duration coverage retains its
stricter eligibility; new_missing_progress reports comparison coverage separately
so contract-change contributors remain accessible. No persistence, migrations,
new limits, execution, notifications or VM lifecycle changes are introduced.

### Workflow reporting-gap queue

`condition_gap_queue` uses the existing contributor routes with ongoing cohort,
current comparison cohort_token and saved baseline_token. Scope, baseline time,
comparison eligibility, definition lookup and current fingerprint checks match
condition_gap_owner. Optional gap_status and owner or unassigned=true filters
select matching gaps; both may be absent. Other group selectors are rejected.
Every eligible workflow appears once, independent of the 64-group display cap;
empty filter results are valid. Membership and coverage use the current retained
sample, not original saved cohort membership.

Each contributor exposes gap_queue_priority with full current gap/category
counts, sorted complete assigned owner list, unassigned count and all pinned
missing expectation guidance before filtering or previews, bounded by the
existing 256-condition comparison limit. Guidance includes category and current
expectation without prior facts. gap_progress instead contains exact filtered
counts and the canonical first-16 matching changes, including prior evidence.
Customer responses omit tenant identifiers as before. Ranking uses observed
report-only blocked duration with stable workflow identity ties and an explicit
explanation; intervals spanning contract boundaries are omitted. Neither gap
age, causation nor business severity is inferred. Comparison coverage remains
separate from stricter generic duration coverage.

The dashboard offers a snapshot-bound queue, category/owner filters, one row per
workflow, full guidance and links to existing returned category/owner groups,
reporting Operations and workflow history. Group links retain both tokens;
undisplayed groups lack links but their guidance and counts remain available.
SDKs and OpenAPI expose the queue dimension and priority payload. No new SQL,
persistence, migration, limits, notifications or execution are introduced.

### Reporting context in transition readiness

Existing transition readiness, action previews and the workflow overview now
include workflow-wide current-state condition reporting context and retained
pending resolution proof advisories. Existing declared policy, milestone,
effect, invariant, dependency and blocker guards alone determine Ready. Reporting
expected_states are not transition guards, and missing, overdue, failed or
unknown condition reports do not invent new guards. Planned decision evidence
may satisfy existing requirements but does not replace retained reporting context.

The optional reporting payload uses the latest reporting Operation's immutable
pinned workflow condition declarations and the existing full condition coverage
evaluator. It returns compared/unconfigured/terminal or explicit unavailable,
incomplete, ambiguous and limit statuses. Counts precede the existing first-16
preview and full 256-condition identity checks. Conditions refer to current
state, not proposed destination state. Explicit decision replacement, metadata
preservation, contract boundaries, step identity and source-pinned freshness
match performance coverage. Incomplete histories cannot imply missing or
satisfied reporting; unconfigured means no declared current-state expectations.

Pending_verification_count counts retained awaiting resolution proofs targeting
the transition Operation across retained workflow reports before canonical
first-16 previews. Other targets and verified proofs are excluded. Declared
verification owners, milestone references and resolution reporting Operations
are exposed with explicit truncation. These historical retained obligations are
advisory unless an existing declared prerequisite independently prevents readiness.

MemStore and PgStore compute reporting context once per selected workflow at the
shared projection clock, including readiness-only requests; all transition edges
reuse it. The existing scoped SQLC bottleneck history projection now includes
explicit decision/path contexts and pinned condition metadata matching memory.
Current definition lookup reuses the scoped batch definition reader. There is
no dependence on fact/history page cursors and no per-edge queries. Internal
full verification/context slices are omitted from snapshot JSON; public evidence
contains no tenant override. Dashboard guidance and evidence Operation links
explain the findings without granting execution or authorization. No persistence,
migrations, new limits, business evaluation or VM lifecycle changes are introduced.


### Transition readiness queue across ongoing workflows

`GET /v1/apps/{slug}/workflow-readiness/queue` (account read scope plus MFA)
and `GET /v1/platform-tenant-self/workflow-readiness/queue` (customer Operations
read scope) require an explicit environment and app. The customer endpoint derives
ownership from authentication and rejects customer overrides. The dashboard at
`/dashboard/apps/{slug}/customer-operations/readiness` links each transition to
its workflow readiness detail and current reporting Operation.

The queue samples up to `OperationWorkflowPerformanceCohortMax` (100) recently
updated retained ongoing workflows, across workflow names unless filtered. The
SQLC and memory readers sort by update time descending then the existing stable
workflow identity digest descending. Within each unchanged sampled workflow,
matching current-state edges sort by Operation/from/to and evaluate up to
`OperationHistoryPageMax` (100), reusing one readiness-only milestone projection
per workflow. Evaluation supplies no planned milestones, decisions, effects or
invariants. Existing declared requirements alone determine Ready; current-state
reporting and pending resolution verification remain advisories.

`target_operation` filters before edge sampling. `owner`, `unassigned`, and
`requirement` filter evaluated transitions before the row limit (default 20,
maximum 100). Owners come from blockers or all retained pending verification
obligations targeting that transition Operation, before proof previews. Reporting
refresh owners are excluded. Unassigned requires an actual ownerless blocker or
pending proof; a transition with no owner-bearing finding does not qualify.
The bounded pending-proof preview exposes verification owners and expected milestone
references with resolution and verification Operation links; preview truncation
is explicit. Requirement accepts existing readiness reason codes and never advisory codes.
Unmet rows precede ready rows while preserving source sample/edge order. Counts
cover evaluated matching transitions before row truncation. Workflow sampling,
edge sampling and row truncation are independent and explicit, including empty
results and sampled workflows with no matching edge.

This is a live bounded investigation view, not a cursor or cohort snapshot. The
sample time and completion time are exposed, with per-row shared readiness
clocks. Workflows whose current Operation/report/revision/contract changed between
candidate selection and projection are excluded and counted. Refresh before
acting; application authorization and execution remain application-owned. No
new persistence, migrations, limits, business evaluator or VM lifecycle changes
are introduced. Go, Node and Python SDKs expose the scoped queue contract.


### Saved transition readiness comparisons

The readiness queue now returns a versioned caller-held `snapshot` of its returned
rows. `POST /v1/apps/{slug}/workflow-readiness/compare` and
`POST /v1/platform-tenant-self/workflow-readiness/compare` compare that snapshot
with fresh scoped readiness-only projections of the same workflow instances.
Account routes require read scope and MFA; customer routes derive tenant ownership
from authentication and reject overrides. The scope fingerprint binds account,
app, environment, tenant selector and role. It is a query binding, not a signature
or authenticated historical proof. Caller-provided baselines remain advisory and
cannot authorize execution. Every current fact is read under current caller scope.

At most `OperationHistoryPageMax` (100) saved edges are accepted. The new centralized
`OperationWorkflowReadinessComparisonBodyMaxBytes` bounds the JSON body at 1 MiB;
strict decoding rejects unknown fields and duplicate JSON keys. Baselines validate
format, ownership selectors, identity names, unique edges, reason/advisory codes,
ready/reason consistency, digest shapes, revisions and capture/evaluation times.
Each distinct saved workflow is projected once, independent of the recent-workflow
sample and original owner/requirement filters. This means a blocker clearing is
still compared when the row disappears from an owner-filtered queue.

Only an exact target Operation/from/to edge that remains applicable to the current
state with unchanged pinned version and declared requirement fingerprint can be
`newly_ready` or `newly_blocked`. Other comparable rows are `changed` or `unchanged`.
Contract/requirement changes, terminal state, missing retained state, unavailable
current-state edges, and older retained revisions (`baseline_ahead`) count as
unavailable comparisons and never imply resolved requirements. Added/resolved
reason codes explain guard changes; full canonical blocker, prerequisite, pending
verification and reporting summaries identify evidence categories that changed.
Retained freshness evaluation clocks and elapsed-age counters are excluded from
fingerprints, while evidence identity, invalidation and expiry status are included.
All pending verification obligations contribute before previews. Verification and
reporting advisories cannot independently change Ready. Each row links to the
saved/current reporting Operations and detailed evidence.

`change` filters (`newly_ready`, `newly_blocked`, `changed`, `unchanged`, `unavailable`)
and row limits apply after comparing all saved edges. Counts cover the full saved
view. The response supplies the next available-edge snapshot before filtering;
unavailable edges are omitted. New edges outside the saved view are not enumerated.
Reads remain live rather than atomic, and per-workflow evaluation clocks remain
visible. There is no historical reconstruction or durable snapshot store.

The dashboard saves snapshots explicitly in browser-tab session storage, supports
clearing or replacing them, and compares through the scoped read-only API. Account
or scope changes clear incompatible saved views. Dynamic findings use DOM text
nodes and encoded same-origin links under the existing CSP nonce. Go, Node and
Python clients expose comparison requests and results. Application authorization,
business decisions and execution remain unchanged; no migrations or VM lifecycle
changes are introduced.

Saved readiness comparisons now include a compact inventory of at most 16 evidence observations per edge (the existing milestone declaration preview cap). Stable identities preserve retained prerequisite group/path selectors, selected workflow dependency references and verification obligations. Dependency observations retain status and source Operation without moving age counters. The inventory covers blockers, retained milestones, selected dependency references and verification obligations; planned policy, invariant and effect evidence continues to use the existing reason/category summaries. Individual changes expose owners, retained status, evidence Operations and invalidation Operations. Both inventories must be complete before absence establishes blocker clearance; missing proof is always unavailable, and fulfillment requires a retained verified obligation. Older version-1 snapshots remain accepted with unavailable evidence detail. Contract/state boundaries suppress individual deltas. Full category fingerprints remain independent of previews. This advisory comparison does not authorize execution.

Durable workflow readiness subscriptions and inboxes extend the saved comparison surface. Account subscriptions require deploy-write scope and MFA for creation/deletion; account reads require read scope and MFA. Customer subscriptions require Operations manage scope for creation/deletion and Operations read scope for inbox/list; their tenant identity derives from authentication. Subscription and inbox lookups fence account, app, environment and account/customer role, with optional account-only tenant narrowing. Applications and selected customers must belong to the account. The shared app budget is 20 subscriptions, enforced under the account lock. Configuration bodies are capped at 4096 bytes.

The apid worker polls every 30 seconds, claims at most five due subscriptions using a five-minute lease and SKIP LOCKED, and evaluates each with a one-minute timeout. Creation initializes a fresh server-read baseline and emits no invented readiness change. Every evaluation compares previously saved edges independently of owner filtering, then discovers matching recent queue edges; discovery is capped at the existing 100-workflow cohort and 100 saved-transition budget. Fresh comparison observations take precedence over earlier discovery reads. Newly ready, newly blocked, retained owner reassignments and unavailable observations are selectable notification kinds. Newly unknown dependency evidence and removed retained proof also report unavailable evidence rather than fulfillment. Reassignment that also changes readiness is represented by the highest-priority selected kind, with both changes preserved in its evidence. Owner-filtered work is compared through its reassignment before leaving that watch. New edges initialize observations without claiming a previous blocked state. This is periodic bounded observation, not capture of every transient business change; narrowed selectors improve coverage.

A single transaction commits the new baseline, inbox sequence, notifications and retention pruning under the current lease token. Worker retries or concurrent claimers cannot publish the same comparison twice; deletion cancels outstanding commits and cascades inbox removal. Unchanged evaluations create no entries. Repeated identical unavailable states and evaluation failures are suppressed until recovery or a meaningful observation change. Failed evaluations preserve the last successful baseline and show retrying status. Evidence previews remain capped at 16; full category fingerprints still detect changes. If changed blocker/verification evidence cannot support complete individual ownership explanations, an unavailable notification reports that limitation rather than claiming a reassignment. Coverage flags report discovery, preview and saved-edge limitations. Inboxes retain the latest 100 notifications per subscription, expose ascending sequences and explicit resync-required gaps, and provide bounded pages. Dashboard controls create/delete watches and incrementally read inboxes; the worker continues when browsers close. No external destination is contacted and no workflow or Operation is executed. The schema migration is shipped for review and is not applied by this implementation workflow.


Readiness notifications now support shared inbox coordination: unread, read or acknowledged status, a follow-up owner (256 UTF-8 bytes), note (1024 UTF-8 bytes), optional UTC follow-up time, and authenticated actor/timestamps. These values describe human coordination, never application blocker resolution, readiness or execution permission. They are shared within the subscription namespace, not individual user receipts; session actors identify the authenticated account and bearer actors identify the API key. Updates replace the complete coordination record and require the current revision (zero for an untouched entry); stale updates return conflict. A repeated acknowledged edit preserves the original acknowledgment actor/time; leaving that status clears them. Clearing the follow-up time removes pending work. Past times are permitted for overdue follow-ups.

Coordination is stored in the existing durable notification JSON payload, so no additional migration is needed. PostgreSQL updates lock the ownership-fenced subscription before the notification read/write, serializing worker pruning, deletion and other editors. Memory storage uses the same scope fences and revision semantics under its mutex. The original notification identity, sequence, evidence and delivery cursor never change. Coordination expires with the latest-100 notification retention budget; expired entries return not found. Incremental delivery does not redeliver coordination edits, so dashboard refresh reloads retained records before resolving edit conflicts.

GET of an individual retained notification checks its saved edge against current evidence using the existing comparison evaluator. Ready/blocked is reported only for a comparable current edge; contract, state and retention boundaries remain explicit. Global evaluation-failure notifications remain unavailable. The original notification is returned unchanged, and acknowledgment never affects this check. Account check reads require read scope and MFA, coordination writes deploy-write and MFA; customer reads and writes require Operations read/manage respectively. Customer identity remains authentication-derived. Dashboard pending/owner filters apply only to loaded retained entries and do not claim an exhaustive task inventory. Follow-up owner is a coordination label, not an asserted platform identity. SDKs expose both update and current-evidence check.


The readiness follow-up work queue aggregates pending coordination across the app/environment's retained subscription inboxes. Account readers require read scope and MFA; customer readers require Operations read and remain fenced to their authenticated tenant and customer subscription namespace. An account-only customer selector matches each notification's saved evidence tenant, falling back to a customer-specific subscription tenant for global evaluation failures. It therefore includes matching work from broad account watches without reading customer-created inboxes. Owner is the exact coordination owner label, distinct from application blocker ownership. Workflow and all/overdue/upcoming selectors apply before pagination. An acknowledged notification with a follow-up time remains pending until that time is cleared.

A single PostgreSQL join projects compact follow-up rows rather than full evidence payloads. The memory backend captures equivalent projections under its mutex and deep-copies nested coordination. The read budget is the existing 20 subscriptions × 100 retained notifications = 2000 candidates per app; pages reuse the 20 default/100 maximum history page limits, and offsets range from zero to 2000. No new storage or migration is required. All retained matching candidates are filtered and sorted by follow-up time, subscription UUID and sequence before counts and pagination; a single response clock classifies deadlines at or before that time as overdue. Ascending deadlines put overdue work first. Account responses include both the evidence tenant and original subscription tenant for correct inbox references; customer responses omit both tenant fields.

Rows preserve subscription/notification identity and revision for optimistic reschedule/clear writes and link to the original inbox plus its current-evidence check. Reads do not evaluate every workflow or infer readiness from acknowledgment, assignment or due time. Identical workflow edges from distinct inboxes remain distinct coordination records. Counts cover matching retained notifications only, and retention_limited is always explicit. Offset pages are live and may move during edits or retention pruning; consumers refresh after changes rather than treating this view as a durable task inventory. Dashboard updates reschedule/clear through the existing management-authorized API, preserving the other coordination fields, and reload the queue after success. No external notification destination, escalation, business action or VM lifecycle change is introduced.


Due follow-ups now receive one durable in-app reminder marker on their original notification, rather than another notification row. Reminder identity is a stable namespace UUID derived from the source notification UUID and normalized UTC deadline. The marker records deadline and successful publication time; retries, replicas, restarts and scheduling the same deadline again retain the same identity. No delivery sequence, business evidence or coordination revision is advanced by publication, so reminders neither create recursive follow-ups nor consume additional inbox retention capacity. This is an active reminder view, not a new historical event stream: rescheduling or clearing removes the marker immediately, and reusing a retired deadline may recreate the same stable identity with a new publication time. Changing notes, owner or acknowledgment with an unchanged deadline preserves its reminder. Acknowledgment alone does not complete pending follow-up work.

A separate apid publisher runs immediately at startup and every 30 seconds, publishing at most 100 due reminders per pass with a 10-second timeout. PostgreSQL locks eligible notification rows with FOR UPDATE SKIP LOCKED, rechecks the locked payload and atomically commits the batch. Coordination edits continue to lock the ownership-fenced subscription first and now lock the exact notification row before reading its payload, preventing a concurrent edit/publication from losing either value. The publisher locks notification rows only, so it never reverses the subscription-before-notification lock ordering. Memory storage performs the equivalent bounded writes under its mutex. Clearing, rescheduling and retention pruning serialize with publication. Failed PostgreSQL passes roll back and retry; batch backlog can delay reminders, and overdue count does not imply a marker has already been published.

Existing follow-up reads expose optional reminder metadata, a filtered due_reminder_count before pagination, and reminder_only=true for the active due-reminder view. Active means the retained marker matches the current follow-up deadline and that deadline is due at the response clock. Account read/MFA and customer Operations-read boundaries remain unchanged, including customer/environment/owner/workflow selectors and original inbox references. No fresh application readiness is inferred or automatically evaluated by the publisher. The dashboard refreshes a compact due-reminder count while visible, offers a dedicated filtered view and original-notification/current-evidence links, and uses the existing revision-checked reschedule/clear controls. Inbox refresh also displays the marker; incremental notification sequence reads do not redeliver these metadata changes. Reminder state shares the original latest-100 notification retention window, and no new migration, external destination or application execution is introduced.

Dashboard deadline editors preserve the complete stored timestamp when the displayed input is unchanged, including precision finer than browser date controls. Notes/acknowledgment edits therefore do not silently round deadlines or retire a reminder; explicitly changed dates use browser-local input converted to UTC.

Individual readiness notification checks now return an advisory workflow handoff alongside the existing original notification and current comparison. The brief is assembled from that same checked observation, without an additional evaluation or business decision. Comparable checks expose current blockers, application-reported owners and next actions, prerequisites, pending verification and evidence changes through the existing typed comparison; the dashboard renders those details with reporting and evidence Operation links. Coordination owner, note and deadline remain explicitly separate from application-reported ownership.

The brief labels its baseline as the saved notification observation. Existing follow-ups do not capture scheduling-time workflow evidence, so the API and dashboard explicitly disclose this limitation rather than claiming changes since scheduling. Global failures stay unavailable; contract/state/retention boundaries suppress current-requirement and evidence-delta presentation. Incomplete evidence, truncated verification details and non-current reporting remain explicit. Missing facts never prove resolution, and stale reports cannot authorize execution. This extends the existing read-authorized, tenant/environment-fenced endpoint and SDK response, requires no migration and performs no writes.

Readiness coordination can now complete a pending follow-up with an asserted outcome (`resolved`, `no_longer_applicable`, or `handed_off`), a required nonblank note (the existing 1024 UTF-8 byte budget), and up to eight distinct evidence Operation UUIDs. `resolved` requires at least one evidence link. These links must resolve to retained Operations belonging to the same account, app and environment; notification workflow evidence additionally fences them to its customer and business subject. Customer identity remains authentication-derived. The existing management-authorized PATCH accepts only completion request fields; completion actor, time and evidence observation are server-derived. No business state, blocker resolution, verification or execution authority is written.

Completion runs the existing current-evidence check before the coordination write and retains its checked-at time, comparison status, reporting Operation/revision where comparable, remaining reasons, pending-verification count, completeness and warnings. Contract/state/retention/global-failure boundaries remain explicit. An asserted resolved outcome can coexist with blockers or unavailable evidence; warnings explain that coordination completion does not establish workflow readiness. The observation is advisory and may precede later application reports; it is not an atomic business-state assertion or an execution guard. Evidence Operation references may expire later under their existing retention rules.

The existing subscription/notification lock order and optimistic coordination revision protect completion against concurrent edits, other completions, pruning and reminder publication. Saving completion clears the follow-up deadline and reminder atomically, removing it from pending queues. The original notification, sequence and evidence remain intact. The completion record is immutable and preserved through later note/owner/acknowledgment edits; completed notifications cannot be scheduled again or have their outcome overwritten. Clearing a deadline without completion still records no completion outcome. Records share the original latest-100 inbox retention window and are not a permanent audit ledger. Both inbox and follow-up queue provide evidence preview and completion controls; original inbox reads and handoff briefs show the saved outcome and any remaining evidence concerns. The existing API/SDK DTOs carry the request and stored record; no new route, SQL query, migration or external delivery destination is needed.

Completion can opt into a verification-aware follow-up watch. The server captures up to the existing 16 pending-resolution preview entries from the same comparable pre-completion observation. Captured resolution identities include the original blocker/report/revision and named proof Operation/milestone; they are not selected again from a later work queue. The watch records the captured pending count and explicitly marks partial coverage when additional pending resolutions were truncated. Opt-in without comparable current pending evidence conflicts rather than silently creating an empty watch. Existing completions and completions without opt-in remain unwatched.

The subscription worker rotates through at most ten retained source watches per subscription pass, independently of discovery/owner/event filters, reading scoped workflow projections with their full internal resolution-verification inventory. Verified requires the exact named retained application-reported proof for the saved resolution. Missing resolution/proof, stale current state, contract changes or older retained revisions become unavailable; absent proof is never success or invalidation. Invalidation here specifically means a fresh later application state report explicitly reopened the watched Operation/blocker code after the watch began. It permanently invalidates that saved resolution; a later replacement resolution requires a new watch. Watches can continue through state transitions and terminal states while the saved proof remains comparable within the captured contract. These are periodically observed application reports, not an event-complete ledger; changes between polls may be missed. They grant no business decision or execution authority.

Changed verified/invalidated/unavailable dispositions produce new durable inbox notifications linked to the watch and original completion, with the original workflow identity and saved/current reporting Operation links. Watch deliveries are explicitly opted into at completion and do not depend on the subscription's discovery-event choices. Unchanged dispositions are not redelivered; recovering to awaiting-verification updates the source observation without a completion notification. Verified proofs continue to be observed for later loss or reopening. Watch metadata has its own revision; publishing does not modify the immutable completion or shared coordination revision. Original inbox refresh shows current watch observations, while delivery sequences advance for new notifications.

Worker finalization locks the lease-fenced subscription before exact source notification rows, rechecks source UUID/watch UUID/revision, preserves concurrent coordination/reminder changes, then updates observations, allocates contiguous notification sequences and commits publication plus subscription progress atomically. Memory storage uses the equivalent mutex-protected update and JSON isolation. A lost lease, pruned source or stale watch cannot publish a spurious delivery. Infrastructure read failures retain observations and retry without inventing unavailable business evidence. Rotating cursor progress and notification/status changes commit together, so retries and replicas do not duplicate committed disposition changes. Watch reads and completion writes retain the account/customer/environment/MFA authorization boundaries of their existing endpoints.

Watches and deliveries share the original latest-100 inbox retention window; deleting a subscription or pruning a source stops its watch. Delivery can itself advance retention, so source links may later be unavailable. This is a bounded advisory inbox, not permanent monitoring or audit storage. The dashboard offers opt-in completion controls and displays partial coverage, last evaluation time, dispositions and evidence links. Go, Node and Python DTOs expose watch metadata and delivered verification changes. Only a SQLC lease-lock query is added; no new table, migration, external destination, automatic follow-up, VM lifecycle change or application execution is introduced.

Current-evidence checks and handoff briefs recognize the workflow carried by verification deliveries as well as discovery notifications; global evaluation failures still have no synthetic workflow. This lets users inspect current transition evidence from a verification notification without treating the delivered proof status as readiness. Reporting/invalidating Operation links include the exact observed state revision. Any explicitly scheduled follow-up on such a delivery still requires user coordination and the normal revision-checked completion path; watches do not create follow-ups themselves.

Reopening detection compares observed blocker presence as well as the captured revision. A blocker already active when the watch begins does not become newly invalidated merely because another report advances the revision or carries that blocker forward. Only a fresh comparable observation changing the saved Operation/code from absent to active can establish this watch's reopening disposition. Unknown/stale reads preserve the last comparable presence, and the periodic-observation limitation remains explicit.

After a watched proof has been observed verified, loss of that proof remains unavailable on subsequent polls. The last verified observation is retained in the watch item; an ordinary awaiting-verification projection cannot silently turn historical proof loss back into initial pending verification. Only a matching retained verified proof can restore the verified disposition.

Follow-ups explicitly scheduled on verification deliveries project their source workflow, subject and account-only evidence tenant into the existing work queue. Customer/workflow selectors therefore preserve the same filtering behavior as discovery-notification follow-ups, including customer-specific events from broad account watches. Source subscription identity still determines the original inbox URL; no follow-up is created automatically.


### Follow-up business outcome correlation (2026-10-11)

Reuse existing explicit application-reported terminal outcomes rather than create a second outcome reporting model. Completion optionally accepts `expected_outcome_code`, a lowercase terminal outcome slug (maximum 64 bytes, existing workflow state-name limit). The caller selects the desired business result; application contracts and reports remain authoritative. Closing coordination never manufactures success or failure.

The server reads the original notification's exact app/environment/customer/subject/workflow/instance identity independently of readiness discovery filters and transition availability. It stores an immutable `business_outcome` observation at completion; later notification checks return a separate live observation. The observation carries its own check clock, report and Operation IDs, state revision, contract version, reported state/code/description and occurrence time. Reads are advisory pre-write observations, not an atomic snapshot with readiness evaluation or coordination writes.

`achieved` requires an explicit matching terminal outcome; `different_outcome` means an explicit different terminal code, whose business meaning Gregale does not classify as failure; `pending` means comparable fresh nonterminal state. Missing reports, changed contract, state preceding the saved notification, stale state, and terminal state without an explicit outcome remain `unknown`. Historical terminal reports may establish achieved results, but no causal relationship to follow-up completion is inferred. No monitoring subscription or notification is implicitly created. Completion observations expire with existing inbox retention; later application reports may change the live read. Existing completions without an expectation remain unchanged.

The dashboard offers the optional criterion and labels immutable completion results separately from current check results, with scoped evidence links. Go, Node and Python SDKs expose the same optional fields. No database migration is needed because retained notification payloads already contain coordination JSON.


### Durable business outcome watches (2026-10-11)

A completion may opt in with `watch_outcome` when `expected_outcome_code` is set and the original source workflow exists. The completion observation must be pending or unknown; already reported terminal outcomes do not need a new wait. An optional `outcome_deadline_at` must be a future timestamp, requires the watch, and is normalized to UTC microseconds. These options and the completion snapshot are immutable; existing completions are not retroactively enrolled.

The subscription worker rotates through up to 10 retained outcome watches per pass, independently of verification discovery and owner/event filters. Each evaluator receives the existing one-minute evaluation timeout under the existing five-minute lease. Business result reads retain the exact original account/app/environment/customer/subject/workflow/instance and contract. A monotonic observed revision floor prevents lower retained revisions from implying a new result. Missing/stale/changed-contract evidence is unknown; infrastructure read failures keep prior observations and retry. Changed achieved, different-outcome or unknown statuses create `outcome_achieved`, `outcome_different_outcome` or `outcome_unavailable` deliveries; pending recovery updates metadata without a notification, and unchanged statuses are suppressed. Initial unknown evidence is delivered once. Reports are observed at polling time; transient changes between reads can be missed.

At the first successful poll on or after the optional deadline, if the expected outcome is not observed as achieved, `outcome_deadline_reached` is delivered once. This is a check-time observation, not an inference of business failure or whether a result historically arrived before the deadline. If the first due check observes achieved, subsequent evidence loss does not retroactively generate a deadline notice. The prior successful observation clock records that the deadline was already checked. Failed source reads keep their previous observation and rotate back for retry while healthy sources can still publish. Late achieved results may still be delivered. Watches continue observing retained reports until source pruning or subscription deletion, even after a terminal result, so later evidence loss or correction can be reported.

Outcome watch updates and deliveries share the existing subscription lease fencing, source UUID/watch UUID/revision checks, and atomic source-row update/inbox publication transaction. The latest coordination and immutable completion snapshot are preserved; deleted/pruned sources cannot generate deliveries and sequence allocation occurs only after source validation. No coordination revision changes or automatic follow-ups are created. Existing inbox retention remains at 100 entries, so watch notices can contribute to pruning the source that ends monitoring. Delivery payloads retain the source workflow and application report evidence even when source links expire; manually scheduled follow-ups on delivery notifications retain the same scoped source identity in both backends.

The dashboard exposes opt-in monitoring and a local-time deadline with explicit timezone labeling, separately renders current watch evidence and immutable completion evidence, and links deliveries back to their source. OpenAPI and all SDKs expose the same options and retained observations. No new database migration or business execution authority is introduced.


### Assigned business outcome deadline escalations (2026-10-11)

Outcome-watch completion optionally freezes an `escalation_policy` with a nonempty existing owner/team label, optional UTF-8 note, and `follow_up_after_seconds` in 1–604800 (one week, centralized in limits.go). Existing coordination owner/note byte limits and owner whitespace/control-character validation apply. Policy requires an opted-in outcome watch and future outcome deadline; no retroactive enrollment is supported.

The first unmet deadline notification itself becomes an assigned follow-up: coordination revision 1, unread status, configured owner/note, server system actor, and follow-up time equal to delivery time plus the configured delay. It carries the immutable source identity and deadline evidence. The watch retains a single escalation notification UUID/sequence/time reference. Deadline suppression and source/watch revision guards publish the assigned row and reference atomically under the existing subscription lease and transaction; retries cannot create a second escalation. Pruning the escalation does not recreate it.

Subsequent successful outcome-watch reads refresh a separate `outcome_escalation.current` evidence field on the retained assigned notification and flag the first observed achieved outcome with `success_observed_at`. A later evidence loss or correction updates current status without deleting this historical flag. Updates validate exact escalation notification/watch/source identity under the subscription and target-row locks, preserving all human owners, notes, acknowledgment timestamps, follow-up deadlines/reminders, coordination revisions and immutable completion snapshots. Application success does not automatically acknowledge, resolve, reopen or reschedule coordination. Existing controls handle acknowledgment and completion manually.

Account/customer queues expose this evidence on assigned follow-ups in both MemStore and SQLC projections. Inbox notifications and later outcome deliveries link the escalation to the original watch; source or escalation links can expire. Evidence updates end when the source watch is pruned or subscription deleted, while a retained assigned row can still be manually completed. These are polling observations, not business failure classifications, causation claims or execution permissions. No external messages are sent and no migration is needed.

The dashboard configures policies at completion, labels follow-up delay separately from the outcome deadline, renders current evidence and historical application-success flags, and uses the existing shared acknowledgment/completion interface. OpenAPI and Go, Node and Python SDKs expose policy, escalation reference and live evidence metadata.


### Escalation acknowledgment response targets (2026-10-11)

Escalation policies optionally add `acknowledge_after_seconds` (positive up to the existing one-week delay limit; zero/omission disables it) and a different `fallback_owner` using existing bounded owner-label validation. Fallback requires a response target. The target is frozen at completion and starts at escalation delivery, independently of its scheduled follow-up time and original business outcome deadline. Existing policies remain unchanged.

The existing subscription worker independently rotates through up to 10 retained assigned escalations with awaiting targets per pass. It uses inbox data and does not require a successful business-report read or a retained original outcome-watch source. Both backends recheck exact notification/watch identity and current locked coordination before firing. At the first successful due evaluation, an open unacknowledged escalation emits one `escalation_response_overdue` notice. The source target stores its notice clock atomically with delivery; no new follow-up is implicitly created. The notice retains scoped workflow identity and a link to the assigned escalation, and supports existing manual follow-up controls if subsequently scheduled.

A configured fallback is applied once only when the owner still equals the initial owner and no manual assignment history exists. It preserves status, notes, follow-up deadline/reminder and business evidence, increments the coordination revision to reject stale edits, records the server system actor and assignment change, and never overrides an acknowledgment, completion, cleared follow-up or manual reassignment. The missed-target notice is still delivered if fallback is skipped. Acknowledgment immediately fulfills the original target, even if later marked unread; completion or clearing the follow-up stops it and later rescheduling does not restart it. Human actions after a missed target remain possible; the notice clock stays retained.

Manual and automatic owner changes are retained on the assigned escalation's response metadata (latest 16 changes, centralized limit, explicit truncation). The first acknowledgment clock is retained independently of subsequent shared inbox status. The worker rechecks under the existing subscription/notification locks, so an acknowledgment or closure written before publication wins the race. Coordination actions remain application-independent; business success is not acknowledgment and never automatically completes work.

Response monitoring ends with assigned escalation pruning or subscription deletion. Business evidence monitoring still ends with original watch retention, so a retained response target can outlive its business evidence source. The dashboard shows response status/deadline, first acknowledgment, missed notice, fallback configuration and assignment history on inbox/queue rows, exposes policy controls with delays in minutes, and links response notices to the assigned escalation. All SDKs and OpenAPI carry the optional metadata. No database migration or external messaging is introduced.

### Retained workflow coordination timelines (2026-10-11)

Account and authenticated customer readers can project a chronological coordination timeline for an exact application, environment, business subject and workflow instance. The optional account customer selector is exact: omission selects account-owned workflows, not all customers. Account readers scan broad and customer-specific operator subscriptions and filter each notification's identity; customer readers use authentication-fenced customer subscriptions and cannot override the tenant. Existing read permissions and account MFA apply. Query keys are closed, single-valued and nonempty.

The timeline reads current readiness inboxes without creating another event store or changing retention. It includes delivered readiness and verification/outcome notifications, retained acknowledgments and completions, reminder delivery, bounded escalation assignment changes, response-target notices, and outcome observations. A latest coordination edit and latest watch observation are explicitly snapshots; missing earlier edits are not manufactured. Ordering uses retained server action/observation clocks and stable event identity, never application occurrence clocks or inferred causation. A first success flag without its original proof cannot borrow today's evidence identity; a first acknowledgment without its original actor cannot borrow the latest actor.

Coverage lists independently read source inboxes, earliest/latest sequences, retention gaps and subscriptions deleted during the read. Deleted subscriptions cannot be enumerated after deletion, global unattributed failures are excluded, and pruned notifications/assignment history are not recoverable. The response always declares retention limitations. The source budget is the existing 20 subscriptions and 100 notifications per inbox; assignment history remains capped at 16, operation links at 48, and pages at 100 events (default 20). Offset pagination is live and may move after updates or pruning. The dashboard presents these limitations and local-time clocks with links to source inboxes and available reporting operations. Go, Node and Python SDKs expose the read APIs. This remains coordination visibility: the application owns workflow truth, business success interpretation, authorization and execution. No database migration or external messaging is introduced.

### Workflow case overviews (2026-10-11)

A read-only case API combines an exact business subject/workflow instance's existing application projection (reported state and explicit outcome, blockers, dependency evidence, transition prerequisites and resolution verification) with matching retained open scheduled follow-ups and the coordination timeline. Account requests require an explicit customer UUID: existing business-reference projections otherwise allow broad operator reads, which must never mix identical references across customers. Customer identity derives from authentication and cannot be overridden. Existing read permissions, account MFA and strict timeline query validation apply. Offset/limit select only the nested timeline page.

The overview reports unknown, stale, terminal_reported, no_transition, blocked or ready based on retained current evidence. Missing/stale reports require fresh application evidence; stale terminal reports do not establish current business results. Terminal reports retain explicit application outcome codes without inferring success. Ready means at least one current-state declared edge satisfied evaluated requirements, never execution authorization. Suggestions point to unmet prerequisites, application blocker/verification owners and separate human coordination owners. They neither execute workflow actions nor complete follow-ups.

Matching open follow-ups are filtered by exact tenant, subject, workflow and instance, ordered by due clock and evaluated for overdue status at the coordination read clock. Completed or unscheduled work is excluded. Counts cover the retained matching candidate budget (existing maximum 2000), with the first 100 follow-ups and at most 100 suggestions returned and explicit truncation. Evidence, coordination and timeline are independently read and have separate observation clocks; edits and retention pruning can change the view. Existing projection and timeline coverage flags remain available; absent evidence is not proof of absence or success. Case limitations explicitly disclose retention and consistency boundaries.

The dashboard reuses exact workflow timeline selectors, adds a case overview action, shows reported state/outcome, blockers and prerequisite/verification gaps, open follow-ups/assigned escalations, suggestions, and source evidence/inbox links. Stale evidence is labeled before any readiness assessment. All SDKs expose account/customer APIs, with the customer Node convenience client using the same exact identity selectors. No new persistence, migration, worker, execution authority or external notification is introduced.

### Application-defined versioned workflow playbooks (2026-10-11)

Workflow manifests optionally declare playbooks at the business workflow level; compilation carries them to each resolved operation workflow step. Each rule has a unique bounded ID, explicit positive version, exact match kind/code, required application-authored summary, optional suggested team, bounded evidence checklist and optional HTTPS runbook/application-action links without credentials. The existing immutable definition fingerprint includes canonical playbooks. Empty declarations remain omitted, preserving legacy definitions. Rules and evidence lists are sorted without mutating caller-owned slices; duplicate IDs/evidence and conflicting repeated-step rules are rejected. Limits (32 rules per workflow contract, 16 evidence labels, 1024 summary bytes, 256 evidence-label bytes, 2048 URL bytes) are centralized.

Case projection loads the operation and immutable definition referenced by the current retained application report under the exact account/customer/app/environment/subject identity. It selects the same workflow contract version, never a newer live deployment's guidance. Rules match active fresh nonterminal evidence: blocker code, missing milestone/dependency workflow, pending verification blocker code or unmet readiness reason. Identical repeated rules are deduplicated; invalid/conflicting retained rules suppress guidance. Missing/stale/contract-mismatched evidence yields evidence_unavailable; a missing reporting operation/definition yields definition_unavailable; undeclared rules yield not_declared; available rules may have no current matches. Infrastructure errors remain errors, not false absence. Guidance returns rule and workflow versions plus reporting operation identity separately from generic case suggestions and human coordination. Full retained verification observations are used rather than the public preview; omitted transition evaluations mark guidance as truncated.

Playbooks do not modify blockers, readiness, evidence, follow-up owners or execution authority. Suggested teams are application guidance; checklists are advisory rather than automatically verified facts. The dashboard renders summaries/checklists as literal text and opens only credential-free HTTPS links with opener/referrer protection. No link is fetched automatically and no application action is invoked by case reads. APIs and all SDKs carry declarations and matched guidance; manifest YAML/TOML and operations documentation expose authoring. Existing definition JSON persistence is sufficient; no migration, worker or external notification is introduced.

## Durable pinned playbook checklist progress

Application-authored matched playbooks can start durable, exact-case checklists.
Freeze the full rule, evidence labels, workflow contract version, immutable reporting
definition and original reporting operation. A new deployment never silently upgrades
ongoing work. The case identity includes account, application, customer, environment,
business subject, workflow and instance. Operator and customer views share the same
records under their existing read/write authorization boundaries; tenant-self requests
cannot override customer identity. Account mutations require write scope and MFA.

Starts use deterministic case/definition/rule/version identities and return existing
work on retries. Limit storage to 100 checklists per case, serializing quota checks in
Postgres under the existing account lock. Item edits replace evidence and notes under
an optimistic revision fence. The server records authenticated reviewers and clocks.
Validate operation references against exact scope and subject; milestone references
additionally require the pinned workflow, instance and contract version. A completed
item requires evidence but denotes human review only. Reopening is permitted.

Retain latest item snapshots and the most recent 64 review changes, with explicit
truncation and start/review timeline events independent of inbox retention. Records
cascade with account/application/customer deletion and otherwise remain durable.
No user-supplied reviewer, automatic action execution, readiness mutation or inferred
business success is introduced. Evidence validation describes edit-time checks and
must not imply continuing fact availability or business correctness.

## Live applicability of pinned playbook checklists

Case overviews project a read-only applicability observation for each retained
checklist from the same exact current state and reporting-definition lookup used
for matched guidance. Do not refetch newer deployment definitions or silently upgrade
frozen rules. Require fresh comparable state, and distinguish changed workflow
contracts, changed/removed canonical rules, terminal reports, definition unavailability
and incomplete evaluations. Full-rule comparison includes version, owner, evidence
labels, summary and safe links. An identical rule from a newer reporting definition
can remain applicable, with the definition change explicitly recorded.

A positive trigger match is evidence of applicability even when coverage is incomplete;
absence requires complete relevant coverage. Blockers use the current full report,
verification uses all internal observations instead of its public preview, and
transition-based triggers require the complete current-state readiness overview.
Expose observation identity/clocks, review suggestions and remaining human checklist
work. Keep terminal state, trigger absence, human completion and business success
separate. Existing attachments are not continuously revalidated by this check.

Applicability is ephemeral response metadata on case reads, not persisted progress
or a durable timeline event. It never advances revisions, edits or closes checklists,
changes guidance/history, or mutates application-owned evidence and execution authority.
No new migration or quota is required: checklists remain bounded by existing limits.

## Human review decisions for pinned playbook coordination

Add continue/retire/replace decisions under the existing exact-case, scope, tenant and
write authorization boundaries. Server-derived reviewers and clocks accompany a
required bounded reason and the applicability observation reviewed. Store this
observation as durable decision context, separately from the ephemeral live field;
never treat it as current verification or transactionally consistent business evidence.

Decisions and item edits share one optimistic revision. Continue keeps coordination
active, even under explicit evidence uncertainty. Retire and replace permanently close
source coordination without editing frozen guidance, item proofs, completion counts
or business outcomes. Closed sources reject future decisions and item writes. Omitted
legacy dispositions remain active. No automatic retirement or execution is introduced.

Replacement links a distinct already-started active applicable checklist in the same
exact case and requires its expected revision. Validate source and target revisions
and target active state under the shared memory/account writer lock. Application
applicability remains an independently observed pre-write fact. Active-only targets
and irreversible source closure prevent cycles; subsequent replacement chains preserve
older links. No checklist items or proof are transferred and no reverse write is needed:
case reads derive inverse links from retained predecessor decisions.

Retain the latest decision plus a bounded journal of the latest 32 choices, with
explicit truncation and durable timeline events. Add the journal bound to the central
limits table and timeline projection budget. Existing JSON progress storage suffices;
no new migration or application lifecycle writer is required.

### Clone schema coverage for workflow coordination

The workflow state report's selected-path and change-reason columns remain
operational state. Readiness subscriptions, notifications, and playbook progress
are also registered as operational tables in the environment clone schema
inventory. Clone preflight can recognize their schema without copying retained
customer reports, inboxes, or live coordination records into another environment.

## Live application evidence for pinned checklist labels

Optional evidence requirements bind unique existing labels to milestone,
prerequisite or verification selectors, bounded by the existing 16-item limit.
Canonicalization copies/sorts selectors without changing caller slices and
rejects unknown kinds and incompatible fields. Selectors enter immutable
fingerprints, frozen progress and guidance comparison. Legacy omissions preserve
existing identities.

Exact-case reads reuse scoped retained facts and current-state readiness edges.
Prerequisites retain existing freshness/invalidation diagnostics, including
explicit producer, group and path identities. Verification evaluates all
matching obligations on the exact current report using full internal coverage.
Absent obligations, incomplete coverage and unevaluated edges are unavailable.
Fresh comparable state and unchanged canonical guidance are required.

Per-item checks expose satisfied/missing/stale/invalidated/unavailable, reason,
observation time and report identity. Completed items in active checklists lacking satisfaction
suggest review without changing human status or history. Checks evaluate
requirements rather than attachments and establish no external correctness,
business outcome or execution authority. Both backend writers strip ephemeral
checks from progress and history. Dashboard and SDKs expose the same fields;
no new queries, migration or worker is required.

## Opt-in durable checklist evidence watches

Add exact-case evidence-watch configuration endpoints under existing account
write/MFA and customer operations-manage boundaries. Bind an active pinned
checklist to an existing same-role/app/environment inbox, checking customer and
optional workflow scope. Capture a silent baseline from the existing live case
projection. Configuration uses the expected checklist revision and watch UUID/
revision; polling does not advance configuration revisions or human progress.

Store at most 20 watches in subscription JSON, with five active watches rotated
per pass. Stopped slots can be reclaimed with new UUIDs, preventing stale edits
after slot reuse. Watches outlive inbox pruning but end at subscription deletion.
Failures preserve prior baselines, rotate for retry and do not manufacture business
absence. Status/reason changes produce a single grouped, unread, owner-labeled
inbox notice; clocks, report counters and attachment/metadata changes alone are
quiet. Saved observations and fresh case reads remain independent of application
transactions and cannot authorize execution or establish business success.

Both stores serialize configuration and publication with checklist writers.
Postgres takes the account lock before the subscription lock, matching existing
checklist writers. Configuration invalidates current subscription leases; old
workers cannot overwrite it. Publication rechecks exact checklist identity,
disposition and observed revision. Closed/missing records stop monitoring;
revision drift defers delivery without advancing the baseline. Accepted baseline
updates and inbox inserts share the existing fenced finish transaction/memory
mutex, so retries cannot duplicate an already committed change.

New notifications retain their own exact-case identity without fabricating
readiness transition baselines. Timeline/follow-up projections and completion
reference validation preserve that identity and tenant boundary. Dashboard and
SDKs expose monitoring controls and evidence changes. Existing storage handles
the payloads; no migration, additional worker, external messaging or automatic
application/checklist lifecycle mutation is introduced.

## Pinned playbook business expectations and result observations

Optional expected outcome codes use existing bounded outcome slug validation and
enter canonical rule fingerprints. Starting progress pins the original comparable
state revision; frozen expectations survive subsequent guidance changes. Shared
business-result interpretation now serves both existing readiness completion
watches and playbook case observations, preserving fresh terminal-code semantics.
Missing/stale/contract-changed/older evidence remains unavailable for playbooks;
nonterminal state is pending, and explicit terminal codes distinguish achieved
from different outcome. Human checklist completion and causal attribution remain
separate. Progress writers strip live result metadata while retaining the start
revision and frozen rule.

Existing durable checklist watches capture outcome baselines, including playbooks
without live evidence selectors. Status/reason or comparable terminal-code changes
publish one outcome notice, grouping simultaneous requirement changes. Clocks,
report counters and description changes do not notify. Retain a monotonic comparable
outcome revision across unavailable observations and configuration changes to
prevent retained-report regressions from becoming new results. Existing identity,
lease, writer-lock and progress-revision fences apply to publication and closure.

Case and watch reads reuse scoped evidence without extra queries or worker phases.
Dashboard, timeline, OpenAPI and all SDKs expose expectations, current business
results and grouped changes. Existing playbooks and readiness outcome behavior
remain compatible; no migration, application action or automatic checklist closure
is introduced.

## Pinned playbook outcome deadlines and durable assigned attention

Application deadline policies require a frozen expected outcome, positive bounded
wait, required team label and positive one-week-bounded follow-up delay. Optional
notes use existing limits. Canonicalization validates and copies policy pointers;
checklist start freezes its due time from creation. Legacy omission preserves
existing fingerprints and behavior.

Existing opt-in watches observe the first successful due check. Unmet expected
results create one assigned unread notice, with follow-up time from actual delivery
and a durable reference. Achieved due observations suppress retrospective notices.
Configuration preserves due markers/references and applies the retained-revision
guard before recording achieved fulfillment. A distinct explicitly enrolled watch
has its own delivery semantics; inbox pruning never recreates an existing reference.

The accepted watch update and assigned publication are fenced atomically by
subscription leases, exact case identity, active disposition and observed progress
revision. Retained assigned rows are locked for current-evidence refresh. Both
stores preserve human coordination fields and immutable delivery observations;
first observed late success remains historical through subsequent corrections.
Rejected stale polls cannot refresh escalation evidence. Existing follow-up
projection, reminder, acknowledgment/completion and case attribution are reused,
with the additional evidence payload mirrored in memory and SQL.

Deadlines represent observed need for attention, not inferred business failure,
historical timeliness, causation or execution authority. No new storage table,
migration or worker is introduced. Dashboard, OpenAPI and SDKs expose the frozen
policy, pinned clock, one-time marker/reference and separate live evidence.

### Playbook escalation acknowledgment targets

Pinned playbook deadline policies may configure a bounded acknowledgment window and distinct fallback owner. The window begins at assigned escalation delivery, separately from the original business deadline and reminder due time. Both escalation families use the same retained response model, publication-lock recheck, first-acknowledgment semantics, and bounded assignment history. A missed target emits one durable response notice; fallback changes ownership only while the initial assignment has never been manually changed. Completion or clearing the follow-up closes monitoring; later business success does not.

Response monitoring reads retained inbox rows, so it remains available when application evidence fails or the originating checklist watch stops. Retention and subscription deletion bound its lifetime. Playbook response notices carry exact playbook case identity rather than inventing a transition-readiness snapshot. The response event's readiness source is therefore optional; existing readiness response notices continue to include it.

### Business-aware playbook escalation completion

Assigned playbook escalations support an explicit human business disposition paired with a fresh, exact-instance pre-write observation of the frozen expected terminal outcome. The immutable completion records matches, mismatch, unavailable, or not-applicable alignment. Human closure remains possible with contrary or missing evidence; it is never converted into an application success claim. Infrastructure read errors fail completion instead of silently recording cached evidence. Coordination revision fencing protects human edits and automatic fallback assignment, while application state may advance after the observation. Generic completion compatibility, scoped optional evidence references, and existing acknowledgment/reminder closure semantics are preserved.

### Bounded post-completion playbook reviews

Business-aware completion may opt into a review window of at most seven days, pinned to the coordination write. Review state lives on the retained source notification, independently of immutable closure evidence and checklist-watch retirement. It shares the bounded outcome-worker rotation and source row publication fence, but emits notices for meaningful changes in either direction, including pending, evidence loss and recovery. A durable notice links to the original human disposition and exact scoped workflow identity. Expiration is rechecked at publication, and retention bounds monitoring. Infrastructure errors retain the baseline; monotonic comparable revisions prevent older retained reports from appearing as new business success. No review automatically reopens or rewrites human completion.

### Pinned business outcome acceptance rules

Playbooks may explicitly enumerate up to 16 acceptable terminal alternatives, excluding the primary expected outcome. Canonicalization copies, validates and sorts the list. Explicit empty and absent policies are distinct: empty allows no alternatives; absence preserves legacy reviewer acceptance. The frozen policy accompanies business observations and governs completion alignment and bounded post-completion reviews, independently of subsequent deployments. Acceptance classification never changes the application's reported status or the primary expected-outcome deadline. Contrary results remain observable and human closure remains possible; immutable completion is preserved.

### Pinned business outcome explanations

Optional outcome guidance gives application-defined meanings and suggested human follow-up for the expected code and explicitly acceptable alternatives. The bounded list is validated, copied and sorted as part of canonical playbook guidance. The entire set accompanies business observations, allowing completion and bounded post-completion reviews to select the correct original meaning through later result changes. Matching guidance is exposed only for fresh comparable terminal reports; advisory prose never changes acceptance, deadlines, authorization or execution. Existing records without guidance retain their behavior, and saved closures remain immutable.

### Outcome-specific human follow-up checklists

Pinned outcome guidance may define bounded ordered follow-up labels. The existing progress review endpoint selects these through an optional outcome code and local item index, preserving legacy original-item updates. First review lazily creates one list per declared outcome and captures fresh comparable exact-case terminal evidence before the revision-fenced save. Later edits preserve that report, use existing scoped evidence validation and review stamps, and update independent completion counts. Shared bounded history and timeline entries identify the outcome. Original checklist completion and application state remain separate; retired or replaced progress is read-only. No new action authority or automatic execution is introduced.

### Owned outcome steps with pinned completion windows

Outcome guidance may assign declared steps to teams with completion windows from first list review. Policies are copied, validated, sorted and globally capped at 16 assigned steps per playbook. Step updates retain existing review/evidence validation and revision fencing, with optional manual reassignment. Opted-in playbook watches publish one durable queued notice per pending step and watch, retaining delivery references through configuration changes and inbox pruning. Existing queue and due-reminder infrastructure provide assigned and overdue attention without new tables or worker phases.

Accepted polls synchronize step state and owner under the existing progress and notification locks. Manual inbox ownership changes take precedence and retain bounded assignment history; notes, acknowledgment, rescheduling and completion remain human coordination. Completed step reviews clear queued follow-up on synchronization without fabricating completion evidence. Reopening and stopped watches do not reset clocks or automatically recreate deliveries. Application decisions and execution remain independent.

### Acknowledgment and fallback for assigned outcome steps

Outcome step policies may freeze an optional bounded acknowledgment window and fallback team. Assignment delivery starts the response clock, independently of the pinned step completion deadline. Retained step notifications use the shared response evaluator, first-acknowledgment semantics and one overdue notice. Any recorded ownership change prevents fallback; automatic fallback protects its queue owner from subsequent checklist synchronization. Human coordination and original completion remain separate.

Publication refreshes active source progress under the account-first subscription lock order in PostgreSQL, or the existing memory writer lock. It therefore observes concurrent step ownership and completion without relying on live business reports, then rechecks the retained response target. Response monitoring can outlive watch disablement and remains bounded by inbox retention. No business success or execution authority is inferred, and existing assignments without response policy remain unchanged.

### Pinned prerequisites for outcome follow-up work

Outcome guidance may declare a bounded acyclic graph among its ordered human follow-up labels. Validation rejects unknown labels, duplicates, self-edges and cycles, and copies/sorts graph metadata independently of step order. Ephemeral readiness requires prerequisite reviews to be complete with their dependency chains satisfied. Server-side review validation blocks completing waiting steps while preserving pending edits and existing evidence validation.

Dependent completion clocks start at first readiness during a revision-fenced review; independent steps retain enrollment timing. Watches queue new assignments only while ready. Reopened prerequisites expose waiting/review-suggested status transitively without rewriting completed reviews, restarting clocks, or overriding retained queue coordination. Queue readiness is a bounded retained observation; case readiness is recomputed. Human work dependencies do not change application business state or execution authority.

### Durable prerequisite regression attention

Opted-in playbook watches retain sampled dependency states for started follow-up lists. Enrollment is silent. Accepted polls group downstream ready-to-waiting changes, added blockers and relevant newly reopened ancestors by current checklist owner, falling back to the watch owner for unassigned steps. Durable notices preserve exact case identity and completed-step review suggestions without recreating queue assignments or changing reviews and clocks.

Publication uses the existing progress revision and subscription lease fences, advancing baseline and delivery atomically. Errors retain the prior baseline; stale or closed progress cannot publish. Stable observations remain quiet, and recovery silently establishes the baseline for a later distinct episode. Between-poll transitions are not reconstructed, and inbox retention bounds the saved notice history. Manual queue ownership overrides remain independent.

### Linked dependency recovery attention

Regression publication retains a per-step reference to the latest delivered notice. Accepted waiting-to-ready observations publish recovery grouped by current checklist owner and reference, then atomically clear ready references with baseline advancement. Initial prerequisite completion has no regression reference and remains silent; partial recovery reports only its ready subset. Still-waiting references survive re-enrollment, while re-enrollment does not generate retrospective recovery for already ready steps.

Recovery reuses exact-case watch publication fences and does not modify original reviews, queue coordination or clocks. New eligible assignments use the existing publisher, and retained assignments are not recreated. Notice retention may remove linked rows, so events preserve identities rather than reconstructing missing history. Existing regressions without references are not retrofitted; between-poll transitions remain outside the retained sampling model.

Completed outcome steps now retain a dependency review revision when an ancestor is reopened. This obligation is created transactionally by the existing fenced progress update rather than by sampled watches, so brief regressions cannot lose the required human decision. It survives recovery, excludes unvalidated completion from checklist counts and downstream prerequisite satisfaction, and is cleared only by a new explicit step review. Confirming completion requires ready prerequisites, a note and scoped evidence; marking pending requests redo. Existing ownership, due dates and bounded review history retain their semantics.

Dependency validity obligations now produce separate assigned inbox tasks through explicit evidence watches. Each task captures the original completion and regression revision, with a persisted per-episode delivery reference under the existing lease and progress fence. Review tasks use immediate follow-up attention rather than changing pinned step deadlines. Accepted polls close the task on a source review or superseding regression; prerequisite recovery is insufficient. Routing honors checklist owners, watch-owner fallback and protected manual inbox overrides. Delivery references are bounded at 272 per watch, and sampled delivery and inbox retention retain their existing limits.

Outcome step assignment policies now optionally pin a dependency validity review window and distinct fallback owner. Review deadlines are written at the prerequisite reopening transaction, eliminating clock extension from delayed watch delivery. Existing retained-inbox response workers handle one overdue notice and guarded fallback, but these targets require a source checklist decision rather than acknowledgment or manually closed inbox coordination. The source progress refresh runs under the existing account/subscription/notification fences before fallback. Review targets preserve protected routing and bounded assignment history, and source decisions close them without altering pinned original step deadlines.

Outcome step assignment policies may now pin application-declared review impact: a milestone or declared business outcome, explanatory risk and triage priority. Canonicalization validates bounded text, target codes and priority and deep-copies the policy before fingerprinting. Enrollment copies the frozen declaration onto human checklist items; review notifications retain it with their prior-completion snapshot. Case presentation groups unresolved obligations by protected result and highest declared priority, without rewriting business reports or introducing execution authority. Source review dispositions remove confidence obligations, while original deadlines and protected queue ownership remain independent.

Follow-up queues now accept exact unresolved review impact filters and optional business-priority ordering across workflow cases. The shared retained-candidate projection filters and sorts before counting and pagination for both stores, retaining existing tenant/application/environment scope checks and due-time defaults. Business ordering uses overdue attention first, then pinned priority and deterministic due-time/notification ties. Milestone/outcome code filters require a category, avoiding ambiguous cross-category matches. Coordination schedules and effective owners retain their meanings; triage does not change frozen review deadlines or business outcomes.

Follow-up responses now aggregate business impact over the authorized filtered retained candidate window before paging. Group identity includes workflow and protected result category/code. Counts distinguish source review obligations, scoped workflow cases and inbox delivery tasks, preventing duplicate watches from inflating business review counts while retaining effective owner routing totals. Overdue confidence is any overdue matching delivery of an obligation. Deterministic urgency and volume ordering supports dashboard drill-down without introducing new state or expanding application/customer/environment access. Retention and candidate limits remain explicit.

Readiness subscriptions now optionally monitor exact protected workflow/result groups in their existing application/customer/environment scope. Creation records a silent impact baseline; periodic retained-queue observations emit durable first-critical, first-overdue and source-confirmed tracked-review clearance transitions. Impact state and inbox notices share the subscription lease publication fence. Clearance additionally verifies scoped retained progress, including ongoing matching obligations in tracked cases, so manual coordination and retention loss cannot fabricate confidence. Source tracking is bounded at 64 obligations per watch episode and candidate-window truncation suppresses clearance. Configuration is immutable until subscription recreation, and sampled confidence remains separate from application business outcomes.

Business-impact watch definitions can now pin alert acknowledgment windows and distinct fallback teams. Only critical/overdue notices receive targets; clearance notices stay informational. The existing retained-inbox response worker uses the source notification identity, guarded ownership history and immutable delivery deadline, with aggregate protected-result provenance rather than a fabricated transition baseline. Both follow-up stores project alert context and workflow into attention queues, while review impact aggregation excludes alerts to avoid self-generated monitoring signals. Human acknowledgment and source validity decisions remain separate.

Original risk alerts may now carry explicit human review response ownership, a bounded plan, check-in time and fixed authorized review links. The existing coordination revision fence serializes these edits and bounded actor/time history; claiming also acknowledges the alert and replaces queue attention with the response check-in. Source delivery identities are captured only from matching scoped dependency review tasks, and cannot be dropped during later edits. Human resolution requires a fresh pre-write check of every captured source step, preserves the check time, and removes attention without asserting application execution. Active records prevent generic coordination from silently removing responsibility. Parent inbox retention and later independent review regressions retain their existing semantics.

Active review responses now have optional fallback routing and one overdue notification per explicitly scheduled check-in period. A persisted initial-owner/ownership-change marker protects manual handoffs even when bounded history is pruned or an owner reverts. The retained-inbox worker rechecks active status and the exact check-in before applying marker, routing and notice behind the subscription/notification fences. Automatic handoff preserves the response plan, fixed links and deadline, records a system history event, and changes queue ownership together with responsibility. Resolution or explicit rescheduling stops stale checks; later periods rearm independently. Legacy windows without known initial ownership cannot automatically transfer.


Active response plans also sample their fixed linked reviews before check-ins. The first successful sample establishes a silent baseline. New blocked prerequisites, unsatisfied application-declared checklist evidence requirements, and retired, replaced or unavailable progress produce a durable `review_response_blocked` inbox notice routed to the current response owner. Notices link back to the original plan and identify new blockers by linked review position. Stable blockers stay quiet; a blocker that clears and later returns can notify again. Resolution stops sampling. Plan edits and ownership changes fence stale publication; baseline and notice commit together.

Sampling rotates through at most five retained active plans per subscription pass and reads only their scoped cases. Source read failures retry without advancing that plan's baseline. Baselines are bounded by retained active inbox plans. Evidence checks cover pinned, declared live checklist requirements across the linked playbook, not human attachments or a guarantee that a particular follow-up cannot execute. Observations are advisory samples: they do not change the plan, deadline, review disposition, ownership or application business result, and may miss changes between samples.


Response plans also deliver `review_response_recovered` notices to the current owner when previously observed blockers are positively verified as cleared. Fresh active source progress must expose the linked step before prerequisite or source-availability recovery can be reported; declared checklist evidence must explicitly become satisfied. Missing progress, read errors, absent evidence checks and changed failure reasons cannot establish recovery. Unverified disappearances remain in the baseline for later verification. Partial recovery lists only cleared blockers, and simultaneous new blockers produce a separate warning. The first sample stays silent, stable recovery does not repeat, and notices link to the original plan without resolving it or changing its check-in.


Response records expose a `health` summary in inbox and follow-up responses, with matching SDK types and dashboard rendering. The bounded worker samples active plans and reports `unavailable`, `blocked`, `review_required` or `clear` in that precedence order. It includes remaining blockers (including unverified disappearances), the most recent verified recoveries, and each fixed linked review's latest attempt and last available observation. Returned blockers are removed from the recovery list. No summary is present before the first attempt. Source errors publish unavailable health without advancing blocker baselines or emitting recovery notices; backend error details are not exposed. The last complete check means all scoped reads completed, even if a source was missing or retired.

Health updates preserve human response timestamps, history and coordination revision. Human edits retain the latest observation; resolution freezes it as historical context. Clear observations do not prove workflow completion or business success, and source observations may change between polls. Existing owner and check-in fields remain the accountability record.


Existing active response plans accept optional `decision` intent (`continue`, `revise`, `pause`) with a nonblank reason and the `health_fingerprint` the operator reviewed. The server captures authenticated actor, responsible team, decision time and an immutable health snapshot. Latest decisions remain on the response; explicit decisions also appear in its bounded 16-entry human history. Saving without a decision preserves the latest decision and does not duplicate it into history. Decisions use the existing owner, plan and future check-in fields; pause leaves the response active and monitored. No decision executes, authorizes or stops application work.

Health fingerprints include sampled progress and application state revisions, source availability, blockers and validity obligations, excluding polling clocks and recovery-display history. Legacy observations without a fingerprint must refresh first. Decision preparation re-reads scoped linked sources and rejects a changed fingerprint; publication also checks the stored health digest under the notification lock and existing coordination revision fence. These are advisory pre-write source samples, not an atomic lock on application state: a later or concurrent application report can still change conditions after sampling. The dashboard requests refreshed health when a decision conflicts. Resolving responses remains a separate explicit action requiring source-review clearance.


Active responses with an explicit decision now latch `decision_review` when sampled health differs from that decision's reviewed fingerprint. The existing bounded health worker atomically records the first changed condition and one `review_response_decision_review_required` notification under its subscription lease and source lock. The notice goes to the response's current responsible team and captures the prior immutable decision, changed health and differences in blockers, availability, progress or application-state revisions and review obligations. Read failures can require revalidation without establishing business failure. Polling timestamps alone do not trigger the alert.

Subsequent condition changes and a return to the original condition remain quiet for that decision; live health continues updating. Acknowledgment, ordinary plan edits, owner changes and rescheduling preserve the review flag. A new explicit decision against freshly checked health clears it and starts a new episode. Resolution stops monitoring and retains the flag as historical context. Inbox and queue views show the requirement and link to the response decision form. Original intent, check-ins, human audit history, source review obligations and application execution are not changed automatically. Notifications and response context remain subject to existing inbox retention and sampled observations.


Decision revalidation uses a response-level `decision_review_after_seconds` window, defaulting to 24 hours and configurable from 60 seconds to seven days. Each newly required review pins `due_at` from its original `required_at`; later policy changes affect future review episodes. Legacy flags without deadlines derive and persist a deadline from their original requirement time and effective policy. Human edits preserve the pinned episode before changing its policy.

The retained-inbox worker independently rotates through at most five pending review deadlines per subscription pass, without application-state reads. An active episode that passes its deadline receives exactly one `review_response_decision_review_overdue` notice routed to the current response owner; the deadline and overdue marker commit together under source and subscription locks. A new explicit decision or response resolution wins against stale publication. Acknowledgment, attention edits, owner changes, a rescheduled check-in and condition recovery do not satisfy the review. New decisions reset the episode; resolution ends monitoring and retains historical context.

Follow-up queues expose `attention_due_at`, the earlier of the existing check-in and active review deadline, and use it for due filters, counts and ordering before pagination. Coordination's original follow-up timestamp remains the human check-in, so existing acknowledgment and edit requests preserve their contract. Review deadline alerts are separate from check-in reminders. Dashboard views show both dates and the immutable overdue marker. These are retained, bounded advisory workflows; application execution remains application-owned.


Responses accept an optional `decision_review_fallback_owner` for future revalidation episodes. Request omission preserves it, explicit empty string clears the future policy, and a provided nonempty team must be distinct from the submitted owner and satisfy existing owner-label limits. A newly required review pins its initial owner and fallback alongside its deadline. Existing or legacy episodes do not gain a retroactive fallback; unknown initial ownership prevents automatic reassignment.

At the one-time overdue evaluation, a distinct pinned fallback takes over both response and inbox ownership only if the initial owner remains unchanged, no ownership-change flag was recorded, coordination still agrees, no completion exists, and its revision can advance. Human ownership moves (including a move away and back) and automatic check-in handoffs latch protection independently of retained history. A decision-review handoff also protects the existing check-in period from another automatic takeover. The response history records `decision_review_overdue`, and the episode and alert preserve previous/effective owners and whether reassignment occurred. Failed guards still publish the overdue alert to the current owner. Plans, check-ins, deadlines, immutable decisions and validity review obligations remain intact. A new explicit decision starts a fresh episode; ordinary future-policy edits do not rewrite an active episode.


Handed-off reviews expose explicit acceptance through the existing response update: `handoff_acceptance` supplies the exact episode decision and requirement timestamps plus a nonblank note. The response must stay active, retain the handed-off owner in both response and coordination, and commit a future check-in. Acceptance and a new decision cannot be combined. Preparation and locked publication reject stale episodes, ownership changes, resolved responses and repeated acceptance. Existing account/tenant authorization applies; the authenticated actor records acceptance for the team label rather than proving team membership.

The episode stores immutable accepting owner, actor, time, note and committed check-in. Human history records `handoff_accepted` and retains the acceptance snapshot even when a later explicit decision supersedes the episode, within the existing 16-entry history bound. Ordinary edits retain the original acceptance; later check-in edits or ownership moves do not rewrite it. Dashboard inbox and queue views distinguish a current handoff awaiting acceptance from accepted or historical handoffs. Acknowledgment alone is insufficient, and acceptance does not clear decision review, reset its deadline or overdue marker, verify business completion, or execute application work.


Handoff acceptance uses `handoff_accept_after_seconds`, defaulting to one hour and configurable from one minute to seven days. The actual overdue transfer pins `acceptance_due_at` from the handoff timestamp. Future policy changes and check-in rescheduling do not extend it. Legacy reassigned episodes with a known handoff timestamp derive and persist a deadline from that original timestamp and effective policy; unknown handoff time cannot invent an acceptance clock. Ordinary edits pin legacy deadlines before changing future policy.

An independent retained-inbox cursor checks at most five pending handoffs per subscription pass without reading application state. An active handoff still owned by its assigned team and lacking explicit acceptance receives one `review_response_handoff_acceptance_overdue` alert when its pinned window expires. Publication atomically stores the overdue marker with the notice and rechecks decision/requirement/handoff timestamps, deadline, owner agreement and acceptance under source/subscription locks. Acceptance, a new decision, resolution or an ownership move wins against stale publication. Notification acknowledgment never counts as acceptance. Alerts do not reassign ownership or alter the original decision-review deadline, plan, check-in or business execution. Late acceptance remains allowed and retains its overdue history.

Dashboard inbox and follow-up views show time awaiting acceptance, the pinned acceptance deadline and overdue alert time alongside the original review deadline. Existing queue urgency remains based on the earlier check-in or review deadline; acceptance does not remove an unresolved review from the overdue queue. Monitoring and historical context remain subject to retained inbox bounds.


A current unaccepted handoff can be explicitly declined with matching decision/requirement timestamps and a nonblank reason. Existing account/tenant authorization applies; an authenticated operator records the decline for the responsible team label without proving team membership. Preparation and locked publication reject accepted, already declined, stale, resolved or differently owned episodes. Decline cannot combine with acceptance or a new decision and must retain the owner, plan and exact saved check-in, even if overdue. It does not extend either deadline or automatically return ownership to the previous team.

The episode latches `reassignment_required` and stores immutable decline owner, previous owner, actor, time and reason. History records `handoff_declined`. Acceptance and its reminder clock stop for that declined episode. Manual ownership reassignment clears the routing flag; a new decision supersedes the episode, and retained decline history remains available for audit. The original decision-review obligation remains until a new decision or explicit resolution.

An independent bounded cursor publishes one `review_response_handoff_declined` notice to the captured previous owner and commits its notification marker with the source under the existing subscription lease and notification locks. It scans retained decline history so immediate reassignment, resolution or a new decision does not discard the notification intent. Notifications are historical and link to current ownership for manual routing; no automatic reassignment occurs. Original inbox pruning or the existing 16-entry history bound can limit delivery. Dashboard forms distinguish decline from acceptance and show pending manual reassignment.


A declined handoff requiring routing now accepts explicit `handoff_reassignment` with matching decision, requirement and decline timestamps, a reason, and a different responsible team in the parent owner field. Authorized operators retain the exact plan and check-in (including an overdue one), and cannot combine routing with a decision, acceptance or another decline. Preparation and locked publication recheck the current declining owner and episode. The existing owner-label authorization model applies without proof of team membership.

Reassignment atomically transfers response and coordination ownership, preserves original review requirement/deadline and immutable decline history, and starts a fresh acceptance window at its own `handed_off_at`. The old acceptance-overdue marker is replaced for the new route; the new team is not automatically accepted. Acceptance and decline must supply the current handoff timestamp after manual routing, preventing old route requests from applying to a later route. Legacy automatic handoffs can omit the timestamp. The decision-review overdue marker remains historical and is not restarted. Current routing clears the manual-reassignment flag; a new decline can require another explicit route.

A bounded independent history cursor publishes one `review_response_handoff_reassigned` notification to the new team with reason and prior decline context, committing its marker with the source under existing locks and lease. Later decision changes or immediate re-decline do not discard retained notification intent; history and inbox bounds can still limit delivery. Dashboard forms offer explicit reassignment, show the new handoff clock and required acceptance, and preserve audit records. Plain owner edits remain available but do not create a new handoff/acceptance episode. Application execution and business outcomes remain application-owned.


Future handoffs can configure `handoff_acceptance_escalation_owner`: a control-free team label, at most 256 UTF-8 bytes, distinct from the explicitly submitted response owner. Omission preserves policy and an empty string clears it. Each automatic or manual handoff pins the destination in `acceptance_escalation_owner`; later policy edits do not reroute that handoff. Legacy handoffs without a pinned destination receive their existing overdue notice only.

When an active, unaccepted and undeclined handoff misses its acceptance deadline, the existing bounded deadline worker atomically records `acceptance_deadline_notified_at` and, for a distinct pinned team, `acceptance_escalated_at`, publishing both the assigned-team reminder and one `review_response_handoff_acceptance_escalated` inbox notice. The escalation includes the decision, original review deadline, current route, reason and prior decline snapshot when manually routed. Acceptance, decline, resolution, ownership changes or superseding decisions are rechecked under the existing locks before publishing. No ownership transfer or deadline extension occurs. A destination equal to the current owner is deduplicated to the ordinary notice. A fresh explicit handoff resets escalation for its own acceptance window. Dashboard controls configure future policy and show the pinned destination and historical delivery marker.


An active, escalated and still-unaccepted handoff can record one explicit `escalation_acknowledgment` through the original response. Supply matching decision, requirement, handoff and escalation timestamps, a coordinator label, action plan and future `due_at` within seven days. The parent response must retain its exact owner, plan and check-in, including an overdue check-in. Acknowledgment cannot combine with a decision, acceptance, decline or reassignment; notification acknowledgment alone creates no commitment. Preparation and locked publication validate the episode and reject duplicate commitments. Existing operator authorization applies; coordinator labels do not prove team membership.

The immutable record captures coordinator, plan, deadline, actor and server time in the current review and `escalation_acknowledged` history. An independent bounded cursor publishes one `review_response_escalation_commitment_overdue` notice to the coordinator if the deadline passes while this handoff remains escalated and unaccepted. Final publication rechecks coordination revision, decision and route identity, acknowledgment and deadline. Acceptance, decline, resolution, owner changes or a superseding decision suppress follow-up; a new manual route resets the current commitment while retaining history. The delivery marker is committed with the notice and mirrored in retained history without changing human update times. History and inbox retention bounds still apply. Acknowledgment never transfers response ownership, extends original deadlines, accepts the handoff or satisfies decision revalidation. Dashboard controls distinguish the commitment from acceptance and show the action deadline and delivery marker.


Explicit `escalation_completion` closes one retained coordinator commitment on an active response. Supply exact decision, requirement, handoff, escalation and acknowledgment timestamps, a nonblank action summary (UTF-8, at most 1024 bytes) and one to eight distinct zero-based `review_indices` from the current response. The server copies those saved references into immutable evidence, recording actor and time; it does not infer fresh application verification or business success. Parent owner, plan and exact check-in are preserved, even when overdue, and parent reviews must be omitted. Completion cannot combine with acknowledgment, decision, acceptance, decline or reassignment.

Preparation and locked publication reject stale episodes, changed ownership and duplicate completion. Work can still be recorded after acceptance or decline for the same retained episode; a new route, decision or resolved response supersedes it. The current acknowledgment and its retained history entry receive the completion record, and a separate `escalation_commitment_completed` history entry preserves the audit. Pending follow-up publication rechecks completion under existing locks, so closing the commitment prevents a stale deadline alert. Previously published overdue markers and notices remain historical. Ownership, handoff acceptance, review obligations and their deadlines are unchanged. Dashboard forms distinguish completing coordinator work from resolving the response and link the saved evidence.


Explicit `escalation_action_review` observes only the current completion's immutable evidence references using the existing scoped playbook observation logic. Exact episode and completion timestamps, original owner/plan/check-in and omitted parent reviews fence the request. The recorded actor/time and health snapshot distinguish clear, blocked, review-required and unavailable evidence; source indices refer to completion evidence order. Read failures and unavailable backend produce unavailable observations, never success. Cancellation aborts publication. This is a scoped observation, not proof of causality or business completion, and application state can change after the read. The current completion keeps the latest review and retained `escalation_action_reviewed` history preserves prior snapshots.

When the handoff is still unaccepted and undeclined and its completed action has a recorded non-clear review, explicit `escalation_revision` can create a revised coordinator commitment. It supplies the recorded review timestamp, coordinator, revised plan and future deadline within seven days. Preparation reads completion evidence again and rejects newly clear conditions; locked publication fences current episode, completion and prior review. The new commitment records the fresh basis and previous completion time, resets its own follow-up delivery marker, and appends `escalation_commitment_revised` history while retaining the previous completed work. Repeated revisions form bounded audit history rather than nested chains. Ownership, acceptance and original review/check-in deadlines remain unchanged. All response actions are mutually exclusive. Existing notification and history retention limits apply.


Coordinator completion can opt into one scheduled evidence check with `review_after_seconds` (60–604800 seconds). Server completion time pins `review_due_at`; omission leaves checks manual. An independent timeout and bounded rotation read at most five due retained actions per pass using the same scoped observation as manual reviews. Read failures produce unavailable observations; timeout never infers clear evidence. The check occurs on the next successful worker evaluation at or after its due time, rather than at an exact wall-clock instant.

Locked publication rechecks coordination revision, decision, handoff, escalation, acknowledgment, completion and due time, and current ownership. Resolved responses, superseding decisions/routes or revised commitments suppress an obsolete check. Acceptance or decline alone still permits observation of completed work in the same retained episode. Ordinary plan edits and manual reviews do not consume or postpone the schedule. The worker commits its `scheduled_review_at` marker, fresh evidence snapshot and one `review_response_escalation_action_checked` notice to the captured coordinator together, mirroring retained completion history and recording a system review history entry without changing human update timestamps. Notifications show clear, blocked, review-required or unavailable evidence and never infer causality or business success. A revised commitment remains an explicit operator action; the scheduled check does not change ownership, acceptance or original deadlines. Inbox pruning and bounded history still limit retained evidence.


Explicit `scheduled_review_cancellation` cancels one pending scheduled check on the current completed coordinator action. Supply matching episode, acknowledgment, completion and due timestamps and a nonblank UTF-8 reason (at most 1024 bytes, line breaks and tabs allowed). Parent owner, plan and exact check-in remain unchanged, including an overdue check-in; parent reviews must be omitted. All response actions remain mutually exclusive. The existing authorized operator model applies; cancellation does not require reading application sources.

Preparation and locked publication require an active retained episode, matching current ownership, a scheduled due time and no published or canceled check. Due or in-flight checks can still be canceled before publication. The immutable audit captures original completion/due times, actor, server time and reason in the completion, mirrored history and a `scheduled_review_canceled` change. The worker checks cancellation again under the same lock before committing observation, delivery marker and notice. If cancellation wins, no scheduled result is published; if publication wins, cancellation returns conflict and preserves that historical result. Manual evidence reviews remain available. Cancellation changes neither coordinator completion, evidence, ownership, handoff acceptance nor review deadlines, and cannot restart the schedule. Superseding or resolved episodes reject stale cancellation; history and inbox retention limits still apply. Dashboard forms distinguish a canceled schedule from a published check and retain the cancellation audit.


Explicit `scheduled_review_reschedule` moves a pending evidence check to a distinct future time within seven days. Supply exact current episode/completion timestamps, the current `due_at`, replacement `new_due_at` and a nonblank UTF-8 reason (at most 1024 bytes, line breaks and tabs allowed). Preserve parent owner, plan and exact check-in, even when overdue; omit parent reviews and do not combine other response actions. Published, canceled, superseded or resolved checks reject rescheduling. Due or in-flight checks can be moved before publication without reading application sources.

Preparation and locked publication fence current ownership, coordination revision, completion and due time. The completion retains `original_review_due_at`, the current `review_due_at` and immutable latest `review_reschedule`; each `scheduled_review_rescheduled` history entry records completion identity, previous and replacement due times, actor, server time and reason. Legacy pending checks pin their existing due time as original on first reschedule. Repeated reschedules preserve the original even after bounded history pruning. Matching completion/acknowledgment history is mirrored. The worker's existing revision and due-time checks reject observations captured against an older schedule, even if a later change returns to the same due time. If publication wins first, rescheduling conflicts and preserves that result. Ordinary edits and manual reviews still cannot move the schedule. Cancellation uses the current replacement due time. Completed work, evidence, ownership, handoff acceptance and original review/check-in deadlines remain unchanged. Dashboard forms show current and original schedule times and the change audit.


The exact workflow case overview now returns `next_actions`, `next_action_count` and `next_actions_truncated`. It projects current source follow-ups before the existing follow-up page cap, matching aggregate response plans through their immutable linked review identities (customer, subject, workflow and instance). Matching completion evidence is filtered to the case, and `shared_plan` identifies actions whose source also spans other cases. It reads no additional inboxes and never derives work from historical delivery snapshots.

Rows bring together response check-ins, decision revalidation, unaccepted or declined handoffs, escalation acknowledgment, incomplete coordinator commitments, pending scheduled evidence checks and optional evidence-review/revision suggestions, plus exact-case prerequisite reviews and other follow-ups. Each carries a responsible label, its own deadline/overdue flag, a human/automatic/suggested trigger and source subscription/sequence; available linked review references support inspection. Resolved plans, fulfilled commitments and canceled/published scheduled checks do not recreate completed obligations. Suggested revision requires a current non-clear completion review and an unaccepted, undeclined handoff.

Actions are sorted overdue first, then dated work by due time, with deterministic source/kind ties. The response caps at 100 action rows; count covers retained matching rows before the cap rather than global tasks or workflow count. Existing coordination retention, independent-read and authorization limitations remain. The dashboard shows the unified list, distinguishes scheduled work from human obligations and suggestions, and links to source action controls and case evidence. It does not perform or authorize application actions and an empty retained list does not establish business completion. Go, Node and Python SDKs expose the same projection.


The readiness work-queue endpoint now supports `view=next_actions` with owner, workflow, customer, due, `action_kind` and `action_trigger` selectors. It reuses the exact-case action projection over retained source records without additional case/evidence reads. Shared source actions are deduplicated by subscription, sequence and kind; affected matching cases and evidence are combined, and `shared_plan` warns that source controls can affect other cases. Account customer selectors match linked case identities rather than aggregate notification ownership. Platform scopes remain implicit and omit customer identifiers.

Owner and overdue/upcoming filters use each action's owner and own deadline, after projection; undated items appear only in all-due views. Rows distinguish human obligations, automatic checks and optional suggestions and sort overdue first, then due time and stable source/kind ties. Pagination/counts describe matching action rows, not workflow cases; next-action pages use `next_actions` and leave `items` empty. Offset is bounded by the retained source candidate limit times eleven ordinary action kinds plus at most 128 fulfillment reviews per source (278000); follow-up view keeps its existing 2000 bound. Reminder and business-impact selectors are rejected in action view, while action selectors require that view. Existing follow-up behavior remains the default when view is omitted. Retention and independent live-read limitations remain; live edits can move pages. The dashboard defaults its work-queue selector to next actions, links to existing source controls and evidence, and retains the follow-up/reminder view. Go, Node and Python SDKs expose query selectors and queue rows.


The dashboard next-action queue now supports up to 20 named team-view presets per browser/application context. Presets store only validated filter strings (environment, customer, owner, workflow, action kind/trigger, due selector and page size), never task snapshots, pagination offsets, credentials or application results. Saving an existing name updates its filters; deletion removes only that browser preset. Browser storage can be unavailable, in which case sharing still works. These presets are browser-local rather than server-managed team configuration.

A shareable/bookmarkable `#readiness-work?…` fragment opens the same next-action filters in the existing application dashboard, then performs a fresh authenticated queue read from offset zero. Links carry the application identity, reject a mismatched application context, and do not grant access or alter the account/application endpoint. Unsupported, duplicate, oversized or invalid filter choices are rejected before use, and the backend retains normal query and authorization validation. Shared views can be saved under a local name. Task edits and saved-filter edits remain separate; stale in-flight reads are discarded when a view changes. Other tabs refresh their preset selector on browser storage changes. Filter names and labels are rendered as text; links include filter metadata, so share them only with intended recipients. Existing live-pagination and retained-coordination limitations still apply.


Readiness subscriptions can configure immutable `next_action_digest` policy with a required exact owner/team label, optional workflow/action-kind/trigger selectors, cadence (60–604800 seconds, default 3600) and approaching-deadline window (60–604800 seconds, default 86400). Owner is both action filter and inbox recipient; existing operator authorization applies without proving team membership. Subscription customer/environment scope is inherited while transition discovery filters remain independent. The first digest evaluation is due one cadence after creation. Policy changes require a new subscription; deleting one removes its inbox and fingerprint tracking.

A separate worker timeout reads retained source work once and projects the same cross-case queue. Overdue items, deadlines through the approaching window and unaccepted handoffs qualify; undated suggestions do not. It tracks fingerprints by source subscription/sequence/action kind, including owner, summary, deadline/overdue phase, case identities and immutable review links, excluding polling times. Stable delivered items are not repeated. Newly qualifying, changed, newly overdue or reappearing work can notify. At most 20 changed rows enter one `next_action_digest` notice; undelivered changed rows remain eligible at later cadences. Tracking is bounded by retained queue candidates and retired when work no longer qualifies. Empty/unchanged evaluations are silent and advance cadence; read failures preserve the due time and mark retrying. Evaluation/delivery times and next due time are exposed on the subscription.

Digest fingerprints, cadence state and the unread notice are saved together under existing subscription leases and persistence transactions. Source records are read independently, so digest items are historical snapshots and can change before publication; source links require inspection before action. No source ownership, deadline, acceptance, commitment or business outcome is changed. Digest deliveries are not projected back into source work, preventing notification feedback. Counts cover retained qualifying actions rather than all global tasks, and source/inbox pruning still limits coverage. Dashboard controls configure cadence and filters and render grouped action links; Go, Node and Python SDKs expose policy, delivery metadata and payloads.

Digest entries include source-keyed explanations: overdue, approaching deadline, and/or waiting for handoff acceptance; changed projected fields compared with the last delivered snapshot; and advisory next steps pointing to existing source controls. Snapshots are saved atomically with delivery, not on undelivered evaluations. Existing fingerprint-only records report unavailable history on their next changed delivery; older notices remain readable without explanations. Retired qualifying items lose comparison history, so later re-entry is a first retained delivery. Evidence changes compare linked references, not live evidence health or hidden escalation state. Team filtering can hide reassignment to another owner; absence never proves completion. Explanations grant no execution permission and never mutate source work.

Checking a digest notification reconciles at most 20 saved entries through retained source inbox reads using the original application, environment and customer boundary. Current action projection preserves the subscription workflow filter and omits the digest owner and deadline filters, so reassignment and postponed deadlines remain visible. Results distinguish outstanding, changed, explicitly completed source follow-ups, no longer outstanding, and unavailable. Resolved source coordination returns its immutable completion receipt; canceled or otherwise vanished action projections never imply completion. Reads are independent snapshots and preserve the delivered digest. Expired/deleted/missing or inaccessible source records are unavailable; unexpected backend failures fail the check. The dashboard performs this check when opening a digest and offers refresh. No state writes, ownership changes, execution decisions, or live evidence-health claims occur.

Next actions now carry deterministic obligation identities from source subscription, sequence, kind and persisted epoch timestamps (and dependency-review revision where available). Review requirements, handoff attempts, coordinator commitments and completion evidence checks remain distinct across repeated cycles. Deadline edits and owner changes preserve the epoch; a new response check-in deadline defines a new check-in obligation. Digest deduplication includes identity. Reconciliation distinguishes a replaced obligation from changed details and exposes only lifecycle receipts anchored to the saved epoch. Coordinator completion receipts match acknowledgment time; scheduled check cancellation/reschedule/review receipts match completed-action time; current handoff receipts match the handoff epoch; reassignment receipts match the declined-at epoch. Unanchored historical acceptance/decline entries are omitted. Retained receipts can establish an old obligation's completion even while a newer action is outstanding. Receipt coverage is partial and limited by source history; legacy actions without identity cannot be assigned an epoch retroactively. Source-level resolved closure remains distinct from precise per-obligation lifecycle evidence. No workflow mutation or execution permission is introduced.

Source mutations now save obligation-linked receipts in the same notification JSON as the source update, under its existing lock/transaction. Human coordination writes, leased worker source updates, playbook assignment synchronization and new notification publication share capture logic. New obligations receive assignments; ownership/deadline/plan changes receive linked receipts; removed obligations record matched completion/cancellation or an explicit supersession without asserting fulfillment. A new explicit response decision can fulfill the exact prior latched review. Digest reconciliation prefers retained ID-linked receipts across later source cycles. Each source retains at most 128 receipts with a persistent truncation flag; legacy history is not backfilled, deleted sources remain unavailable, and bounded lifecycle coverage is always explicit. Receipt capture adds no destinations, application authorization or source execution.

The exact workflow case includes an obligation history view grouped by source subscription, sequence and obligation identity. New receipts retain affected case identities at mutation time, including shared plans; history selection uses those saved identities rather than the source plan's current reviews. The case page reuses the retained inbox scan already used for its timeline, including closed source follow-ups, then joins current actions from the independent next-action projection. At most 100 most-recent obligation groups and 40 latest events per group are shown, with counts, timestamps, caps/pruning indicators and bounded-history warnings. Customer views omit tenant identifiers and unrelated case references; operator views require the exact selected customer. Legacy unscoped receipts are excluded, deleted/pruned sources are unavailable, and no current action never proves completion. Existing source controls are linked, with shared-source warnings; no source state or application authorization changes.

Mutation-time receipts now record paired same-source predecessor/successor links. Same-kind removed/new obligations and explicit handoff reassignment, reviewed coordinator revision, or decision-to-check-in transitions can link when their saved case coverage overlaps. A later cycle may link the most recent retained terminal same-kind predecessor. Existing identities reactivated later are not linked again, preventing cyclic lineage. Links retain a recorded decision/reassignment reason when changed in this write, an explicit revised-commitment context, or a clearly labeled mechanical new-cycle explanation; succession never proves business causation or fulfillment. Edge receipts carry only intersecting case coverage, so case views do not expose relationships to unrelated cases. The case history can expand and jump to a related retained obligation; missing/pruned groups remain explicitly unavailable. Links share the source write transaction and existing receipt caps; legacy history is not reconstructed.

Digest lifecycle results include successor edges only when saved edge coverage matches a delivered case and customer identity; unrelated shared-source edges are omitted.

Outstanding case obligations now have coordination assessments alongside a shared current workflow-requirement context. Acceptance, human routing, scheduled worker checks and evidence reviews are distinguished; fresh reports enable review while missing/stale reports remain explicit. Free-form response/check-in and coordinator plans remain fulfillment-unassessed because no transition binding is declared. Workflow context reuses the existing exact-case instance readiness, including missing milestones, prerequisites, policies, blockers and verifications, with links to named dependency cases. Unknown/stale evidence cannot produce a positive transition verdict; partial transition/report/verification coverage remains explicit. Blocking transition requirements do not automatically block human review or ownership decisions, and overdue work remains independently dated. The view performs no new source reads, infers no dependencies from history and grants no application execution permission.

Free-form response check-ins, coordinator commitments and generic follow-ups can now bind an exact obligation cycle/case to a declared current-state transition and pinned contract version. The case page lets users select a declared transition or explicitly clear a binding with a reason. The existing revision-checked inbox PATCH prepares the binding using fresh exact-case evidence and verifies the active source obligation under its write lock. Binding-only writes preserve status, owner, note and deadline and cannot combine with plan/completion changes. Source coordination retains at most 32 case-specific active-cycle bindings; unrelated edits preserve them and new cycles never inherit them. Obsolete cycles can be pruned during later binding writes; their bounded receipt snapshots remain available. Binding changes and clear reasons are captured atomically in case-scoped history. Current assessments distinguish satisfied target requirements, unmet prerequisites and unavailable/changed/stale declarations, exposing the exact readiness evidence. Observations and persistence are independent; binding never authorizes execution, establishes fulfillment, or automatically closes work.

Bound obligation targets are observed automatically by the readiness subscription worker, rotating through at most five retained sources per pass. The first observation establishes a baseline. Later changes between waiting, requirements satisfied and target unavailable create an owner-addressed durable inbox notice with the exact case, cycle, target and observation time. Unchanged observations produce no notice; cleared or retired bindings stop producing notices. Read failures retain the previous observation for retry rather than asserting recovery. Source revision and binding validation fence concurrent edits, and observations commit atomically with delivery. Notices are retained advisory evidence: inspect the current case before acting. Requirements satisfied never completes an obligation or authorizes or executes its transition.

Bound obligations support explicit fulfillment through the revision-checked source inbox PATCH. Supply the full saved binding, a reason, and a reporting operation/report ID. The server verifies that the exact case and obligation cycle remain active, that the binding is unchanged, and that the retained report belongs to the same application, environment, customer and subject. Its operation name, workflow/instance, from/to states and contract version must match the target, and it must be published after binding; metadata-only reports and readiness alone cannot fulfill work. Lookup scans at most 1000 exact-case reports. Evidence is checked independently before the locked source write and retained with the human actor/time and responsible owner; it does not assert current business success. Fulfillment removes only that case/cycle from current next actions and target monitoring, preserving other cases and source plan controls. New cycles remain outstanding. Case history shows the immutable receipt and evidence; saved digest checks count a cycle completed only when all delivered cases have fulfillment receipts. Source coordination keeps up to 128 immutable receipts and rejects further fulfillment at capacity instead of pruning into reopened work. Fulfillment cannot be combined with plan, source completion or binding edits and never authorizes or executes application transitions.

Fulfilled bound obligations can be explicitly reopened with the full saved binding, the exact latest fulfillment time, and a reason through the source inbox PATCH. The source revision and current unsuppressed action projection must still match that case/cycle; cleared or changed bindings, superseded cycles, closed sources and removed cases reject reopening. Other action edits cannot be combined, and owner, status, note and deadline are preserved. The immutable reopening receipt records the actor, time, current responsible owner and original fulfillment identity; original receipts and evidence remain retained. Only this case/cycle returns to next actions and target observation. Re-fulfillment requires matching application evidence published after the latest reopening. Case history exposes reopening controls and receipts, and saved digest checks consult the latest fulfillment/reopening state for each delivered case. The worker rotates through five retained sources and publishes at most 20 pending owner notices per pass, committing delivery checkpoints atomically with durable notices; source revision and receipt validation fence stale observations. Notices retain the reopening observation and link to current case/source controls; subsequent source changes are not hidden. At most 128 reopenings are retained per source; capacity exhaustion rejects further reopening instead of losing prior decisions. Application evidence never automatically reopens obligations or authorizes execution.

The readiness worker now reviews the latest retained non-reopened fulfillment for each exact case/cycle, rotating through five cases per pass even on large shared plans. It checks the original proof or latest reaffirmation against retained operation/report coverage, fresh current state and contract version, and explicitly named resolution/verification evidence. Missing reports or stale/unavailable current state, contract changes, reopened named resolutions and unavailable formerly verified proof flag human review; ordinary workflow progression is not invalidation. Each exact-case report lookup scans at most 1000 reports; outside-coverage evidence is unavailable, not proven deleted. Unexpected read errors retain prior observations for retry. Reasons remain latched even after evidence recovers, and semantic fingerprints suppress unchanged owner notices. Review writes, notices and source-scoped receipts commit together under lease/source revision checks; outdated bases cannot overwrite later reaffirmation or reopening. Fulfillment remains intact and no current task is automatically reopened.

Case history exposes current retained review flags and explicit reaffirmation controls alongside reopening. Reaffirmation requires the original binding/fulfillment identity, saved review fingerprint, reason and fresh full application state report published after review became required. The report must belong to the same application, scope, customer and subject, attest the exact workflow/instance under the fresh current contract, and pass a separate current-evidence observation before the revision-checked write. The server retains the original fulfillment and all immutable reaffirmations, clears only the matching flag, and monitors the new proof thereafter. At most 128 reaffirmations are retained per source; exhausted capacity rejects writes without pruning decisions. Case history and digest lifecycle views filter these receipts to their saved case coverage. Closed source plans can still receive evidence reaffirmation, while reopening continues to require an active source cycle. These are human review receipts, not application execution permission or assertions of current business success.

Pending fulfillment reviews are independent human work items in case and team next-action queues. Their identities include the exact saved case, fulfillment, evidence basis and first-required time; the original obligation remains fulfilled. Owners, latched reasons, evidence-check time and case links expose reaffirm/reopen controls. Reviews have no inferred deadline and qualify for team digests even when undated or their source plan is closed. Digest comparison ignores poll timestamps and ordinary report revisions, while reasons, owner and semantic review changes remain deliverable. Multiple reviews on shared sources retain separate identities and explanations, with customer case boundaries preserved. Each source contributes at most 128 reviews plus ordinary actions; the bounded next-action offset is 278000. Case pages retain their 100-row display cap. Explicit reaffirmation completes the exact review cycle; reopening cancels that review while restoring original work. Closed source coordination alone never counts as completing a saved review. Recovered evidence remains pending until an explicit decision.

Pending fulfillment reviews can be assigned and scheduled independently through `review_coordination`. Supply the review work-item ID, saved review fingerprint, source coordination revision, owner and reason while preserving the source's status, owner, note and follow-up deadline. An empty review owner explicitly unassigns it; omitting the review deadline clears it. The worker preserves these choices for that pending review cycle. New review cycles start without a manually chosen owner or deadline. Dated reviews participate in overdue and due-soon queues/digests; undated reviews remain eligible for review digests. Immutable changes retain the previous and new owner/deadline, actor and reason (up to 128 per source). Stale, resolved or superseded reviews reject changes. Original work ownership and fulfillment receipts remain intact.

An active readiness subscription now delivers `fulfillment_review_overdue` inbox notices for missed explicit review deadlines. A separate cursor rotates at most five overdue reviews per pass, independently of evidence observation and discovery filters. Under the source/subscription locks it rechecks the exact review cycle, saved fingerprint, deadline and active fulfillment; reaffirmation, reopening, supersession and changed deadlines suppress stale work. The notice routes to the current review owner (or remains unassigned) and includes the saved case, reasons and evidence context with explicit reaffirm/reopen controls. Publication and `fulfillment_review_reminders` receipts commit atomically. Each review ID/deadline is delivered once across retries, restarts and restoring an already notified deadline; different deadlines and new review cycles are eligible independently. History retains delivery time, notification ID and saved review owner/context. Reminder delivery does not change the semantic review fingerprint or mark work complete. Coverage remains limited to retained inbox sources and active subscriptions; each source retains at most 128 immutable reminder receipts and stops publishing further reminders at capacity.

Fulfillment review coordination supports optional `escalation_owner` and `escalate_after_seconds` (1–604800 seconds). The team must differ from the review owner and requires an explicit deadline. Omitted policy fields preserve the policy when the reviewer stays the same and a deadline remains, including rescheduling; empty escalation owner or zero grace clears it. Reassignment or removing the deadline clears policy when both fields are omitted. Changes record prior/new policy with the assignment reason and actor; worker observations preserve policy for that pending review cycle, and new cycles start without it.

The escalation grace begins at durable overdue-reminder delivery, so late worker runs do not shorten the response window. A separate bounded cursor checks at most five eligible reviews per pass. Final source/subscription locks recheck review identity, fingerprint, current owner, deadline, policy, reminder receipt and grace expiry before atomically saving one `fulfillment_review_escalated` inbox notice and immutable `fulfillment_review_escalations` receipt. Reassignment after the reminder permanently cancels its escalation window even if reverted; choose a new deadline for a new window. Reaffirmation, reopening, supersession and rescheduling suppress stale delivery. Policy changes may defer or redirect an unsent escalation but never repeat a delivered escalation for the same review ID/deadline. Destination notices retain original reviewer, case, reasons and evidence context; they do not transfer ownership or change fulfillment. Case/digest history includes destination, triggering reminder, scheduled escalation time and delivery time. Coverage depends on retained sources and active subscriptions, with 128 immutable escalation receipts per source; at capacity further escalation delivery stops.
