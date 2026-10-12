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
