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
