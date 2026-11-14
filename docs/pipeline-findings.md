# Pipeline investigation backlog

Investigated 2026-11-12 UTC. The original sixteen findings are **fixed and locally verified** as of
2026-11-13 (Europe/Istanbul). [Validation evidence](ops/evidence/20261112-pipeline-remediation/README.md)
records the completed checks and their limits. The original
reproductions below describe the investigated baseline, not the repaired behavior.
Keep these IDs and their evidence when reviewing or extending the fixes. No
production deployment or provider qualification is implied.

The next agreed customer batch is tracked separately under
[ADR-20261113160716](adr/20261113160716-pipeline-tenant-serving-and-projections.md):

| Item | Customer problem | Implementation and remaining qualification |
| --- | --- | --- |
| CUST-001 | Customer portals need row access per tenant instead of trusting a subject with every row. | Implemented versioned owner-managed memberships, frozen approval, atomic RLS/receipt, source-independent revocation and N-1 claim fences. Local core/native checks passed; full CI tracked in PR #7219. |
| CUST-002 | Mirrors copy source columns customers do not want to serve or store. | Implemented immutable per-table publication/mirror projection, required key retention, projected checks/verification/repair/resync. Local core/native checks passed; full CI tracked in PR #7219. |
| CUST-003 | Generic source setup leaves Neon users with pooled-connection errors and unclear operational costs. | Offline CLI/dashboard guide, direct-endpoint diagnostics and source-aware qualification runner. Actual Neon source qualification remains pending configured staging credentials and reviewed fault controls; the finite WAL byte-cap requirement still applies. |

The next approved batch follows [ADR-20261113181050](adr/20261113181050-pipeline-freshness-durable-cli-and-index-advice.md):

| Item | Customer problem | Implementation and qualification |
| --- | --- | --- |
| CUST-004 | Customers cannot set their own freshness confirmation target. | Optional 15s–24h targets, truthful confirmed/unconfirmed/unknown/inactive status, debounced alerts and monitor version fences. No inferred time lag or automatic read-policy change. |
| CUST-005 | Terminal loss and partial provisioning make setup hard to resume or budget. | Public durable setup API/CLI, resource/billing preview, immutable retry identities, safe recovery IDs and explicit retry/cancel. Temporary discovery refusals no longer freeze idempotency receipts. |
| CUST-006 | Tenant-filtered read queries need mirror indexes without granting application write access. | Structured metadata advice, exact tenant policy expression, frozen approval, bounded transactional DDL and generation receipts. Source-independent work preserves reader priority and replication evidence. |

Local validation and its limits are recorded in [the operability evidence](ops/evidence/20261113-pipeline-operability/README.md). These features remain in the approved staging pilot.

The next approved batch follows [ADR-20261113205500](adr/20261113205500-pipeline-serving-evolution.md):

| Item | Customer problem | Implementation and remaining scope |
| --- | --- | --- |
| CUST-007 | Adding selected fields or tables requires replacing an explicitly projected mirror. | Frozen selection review, exact source-definition approval through API/CLI/dashboard, bounded backfill/verification, projected table copy and API refresh. Removals and incompatible type changes retain explicit recovery boundaries. |
| CUST-008 | B2B portals must manually synchronize employee membership. | Owner-approved Auth app/binding and organization mapping, complete authoritative snapshots, durable reader receipts and one-minute expiring RLS proofs bound to the verified issuer and subject. Revocation is independent of the replication source; JWT organization claims grant nothing. |
| CUST-009 | Portals cannot read related mirrored records. | Declared computed relationships to complete target primary keys, invoker RLS and exact tenant equality, recursive dependency availability checks, typed Data API export, API refresh and generation cleanup. Simple views and one-to-many relationships remain follow-up scope. |

Native checks cover projection/publication replay, projected new tables, Auth issuer isolation, expiry and source-independent revocation, forged RPC arguments, multi-tenant join isolation, dependency quarantine, resync, and newly connected app refresh/retry. Validation and rollout limits are recorded in [the evolution evidence](ops/evidence/20261113-pipeline-evolution/README.md). Review and CI are tracked in [PR #7252](https://github.com/poyrazK/faas/pull/7252).

PIPE-017–018 below record the empty-membership/regrant defect and automatic-grant/control race discovered during this batch. Live Neon qualification, broader recovery-surface parity and verification cost controls remain open.

The next approved batch follows
[ADR-20261114083000](adr/20261114083000-pipeline-scheduled-imports-quality-and-work-budgets.md):

| Item | Customer problem | Implementation and remaining scope |
| --- | --- | --- |
| CUST-010 | Hourly/nightly reporting needs data without a continuously active replication subscriber, sometimes with SELECT-only credentials. | Creation-time scheduled snapshots, a distinct source check, connection-only reservations, bounded staging and verification, atomic publication preserving table identities, private run receipts, capture age and stale-refresh alerts. Existing empty destinations only; live Neon and other provider qualification remains separate. |
| CUST-011 | A correctly replicated dataset can still contain invalid business data. | Scheduled required/unique/range/reference rules, bounded coverage, exact decimal bounds, tenant-equal references, count-only reports and debounced incidents/recoveries. Incomplete or unavailable checks cannot report a pass or clear a failure. No arbitrary SQL, automatic repair or failing-row export. |
| CUST-012 | Customers need predictable source impact during verification and repairs. | Daily UTC windows, lower server query deadlines and per-tick verification/backfill batch budgets. Snapshot/quality manual requests respect the window. Native COPY bandwidth enforcement and larger resumable index builds remain future qualification work. |

Validation and remaining rollout boundaries are recorded in
[the scheduled-work evidence](ops/evidence/20261114-pipeline-scheduled-work/README.md).
The implementation review caught and fixed the following issues **in this new
batch**; these are not claims about production incidents:

| Finding | Fix and durable regression evidence |
| --- | --- |
| A manual snapshot request reused a successful run ID and recovered the old receipt rather than importing fresh rows. | Manual requests clear the run identity; automatic lost-ack retries retain it. `TestScheduledSnapshotRetainsLastGoodCaptureAndRecoversAcknowledgement` checks both paths, exact numeric/JSON preservation, stable table identities and old repeatable-read readers. |
| Snapshot mode could enter a CDC table-addition/removal workflow that its worker cannot reconcile. | Freeze snapshot schema/selection and reject CDC resync/repair workflows. Test the request boundary and retain the separate reviewed-pipeline recovery path. |
| A quality aggregate checked read policies before acquiring its table locks. | Lock dependencies before validating shapes and owned/open policies; quarantined or changed tables yield unknown. `TestScheduledQualityCoverageUnknownAndTenantReferences` checks coverage, privacy and tenant-equal reference semantics. |
| Removing a table used by a quality rule left an invalid saved configuration. | Exclude rule source/reference dependencies from removable choices until the owner removes those rules. |
| Queueing a failed refresh could clear its stale incident before a new capture succeeded. | Preserve failure reason while queued/running and report the next allowed UTC run. The snapshot regression keeps the stale condition while the window defers work. |
| Editing quality rules or work budgets immediately recaptured an otherwise idle snapshot source. | Preserve the capture receipt and next run; reschedule only the interval/window and require an explicit manual run for an immediate refresh. The snapshot regression rejects source access during a quality-only edit. |
| Old workers/monitors could process unfamiliar scheduled intent or falsely recover a quality incident. | Database capability fences for pipelines, checks and monitors; preserve them on binary rollback. `TestScheduledWorkersFenceOlderClaims` and `TestQualityIncidentDoesNotRecoverFromUnknownOrIncompleteScan` cover these boundaries. |

This batch does not enable production or imply that the source provider has
passed the live drill. Simple views, one-to-many relationships, reviewed Auth
detach, native COPY throttling and larger index work remain open follow-ups.

The reviewed baseline is commit `46aad2a12e4d1be39d0948b6942cb18d8e71d0b2` **plus
the existing, uncommitted first batch**: retention-aware alerts, explicit gated-read
errors, and table discovery. Its design is recorded in
[ADR-20261112202000](adr/20261112202000-pipeline-retention-read-availability-and-discovery.md).
PIPE-001, PIPE-004, and PIPE-013 concern that batch and should be addressed before
shipping it.

The first review covered worker reconciliation, native pause acknowledgements, health
and alert projections, saved setup/check arbitration, reader changes, credential
preparation, Data API startup, and CLI creation/recovery. It does not establish
that these are the only remaining issues or qualify a production/provider rollout.

A second pass on the same baseline added PIPE-011–016, covering control
interactions, queued verification cancellation, runtime hook discovery after
startup, verification query plans, CLI UUID handling, and initial-copy monitoring.
The original ten findings are retained below without changing their evidence.

## Queue

P1 means an availability failure to address first. P2 means a reproducible
correctness, performance, or operational issue, or a policy gap to resolve in a
subsequent batch. P3 means a smaller input-compatibility issue. All sixteen
findings have local probes; the evidence limits for the cross-component findings
are described below. The two policy decisions are recorded in
[ADR-20261112220000](adr/20261112220000-pipeline-findings-remediation.md): allow
zero readers for updates while keeping creation at one or more, and require an
explicit CLI resync confirmation.

| ID | Priority | Finding | Classification | Status |
| --- | --- | --- | --- | --- |
| PIPE-001 | P1 | New/rotated read-only credentials lack pipeline hook grants | First-batch integration defect | Fixed; locally verified |
| PIPE-002 | P2 | Health claims pause before the destination acknowledges it | Existing correctness defect | Fixed; locally verified |
| PIPE-003 | P2 | Paused pipelines silently lose WAL monitoring | Existing operational gap | Fixed; locally verified |
| PIPE-004 | P2 | An unrelated discovery search blocks saved setup | First-batch integration defect | Fixed; locally verified |
| PIPE-005 | P2 | CLI wait aborts on the previous revision's failure | Existing recovery defect | Fixed; locally verified |
| PIPE-006 | P2 | Valid digit-led pipeline names produce invalid mirror names | Existing CLI defect | Fixed; locally verified |
| PIPE-007 | P2 | Generated source names collide after truncation | Existing CLI defect | Fixed; locally verified |
| PIPE-008 | P2 | Optional Auth validation happens after resource creation | Existing CLI defect; Auth-enabled path | Fixed; locally verified |
| PIPE-009 | P2 | The final reader cannot be revoked | Policy amended by remediation ADR | Fixed; locally verified |
| PIPE-010 | P2 | CLI resync has no separate destructive-action confirmation | Confirmation policy adopted | Fixed; locally verified |
| PIPE-011 | P2 | Disconnect permanently skips an approved reader revocation | Existing control-interaction defect | Fixed; locally verified |
| PIPE-012 | P2 | A queued verification cannot be cancelled before its run starts | Existing recovery/control defect | Fixed; locally verified |
| PIPE-013 | P2 | A running API never discovers pipeline hooks installed later | First-batch runtime integration defect | Fixed; locally verified |
| PIPE-014 | P2 | Verification discards primary-key order and sorts the source table | Existing performance defect | Fixed; locally verified |
| PIPE-015 | P3 | CLI rejects equivalent spellings of an owned connection UUID | Existing input-compatibility defect | Fixed; locally verified |
| PIPE-016 | P2 | A stalled initial copy has no progress-based health alert | Existing operational gap | Fixed; locally verified |

## PIPE-001 — New credentials can prevent a mirrored Data API from starting

**Trigger and impact.** Install the pipeline read hooks, then create a new
`read_only` binding/login or rotate to a new login. The credential preparation
grants table SELECT but does not grant USAGE/EXECUTE for the pipeline helpers.
The upgraded runtime discovers the helpers, treats the missing privileges as an
invalid installation, and fails startup. A running pipeline can repair the grants
on a later reconciliation; a paused pipeline skips the installer, so it cannot
repair them until resumed. Paused imported rows are supposed to remain readable.

**Evidence.** `TestInvestigationNewReadOnlyLoginMissingHookGrants` installs the
real SQL helpers in disposable PostgreSQL, creates a restricted SELECT login,
and observes helper EXECUTE changing from false to true only after explicitly
running the installer again. This reproduces the privilege gap, not a full
provider credential-rotation/deployment cycle. The startup failure and paused
branch follow directly from the corresponding runtime/worker code.

**Code.** [read_gate.go](../pkg/datapipeline/read_gate.go),
`ensurePipelineReadGate` grants only to roles present when it runs;
[engine.go](../pkg/datapipeline/engine.go), `Engine.Reconcile` invokes it only for
running intent; [credential_roles.go](../pkg/managedpostgres/neon/credential_roles.go),
read-only grants around lines 226–233;
[pipeline-read-gate.mjs](../cmd/gregale/templates/data-api/pipeline-read-gate.mjs),
`configurePipelineReadGate` rejects discovered hooks without EXECUTE.

**Fix target.** Prepare and validate the narrowly scoped hook grants before
publishing a credential, including while paused. Preserve function ownership,
RLS isolation, and the rule that failed helper maintenance cannot block pause or
reader revocation. Acceptance must cover new bindings and rotation during both
running and paused states, including first boot before the next worker tick.

**Implementation (2026-11-13).** Credential preparation now validates and grants the pinned, narrowly scoped read hooks in the same transaction before publishing a read-only login. A shared transaction lock serializes credential publication with an in-flight installation, including when the hooks are not visible yet. Issuance, rotation and replay are covered by `TestBoundedCredentialPreparesPipelineReadHooksBeforePublication`; unsafe helper changes remain rejected by `TestReadGateCredentialGrantIsPinnedAndIdempotent`. `TestReadGateCredentialWaitsForInstallingHooks` proves publication waits for an uncommitted installation.

## PIPE-002 — Desired pause is presented as an acknowledged pause

**Trigger and impact.** Request pause while the destination is unavailable or
before a worker has observed the request. `pipelineHealth` gives paused intent
precedence over freshness and blocked state. It returns `inactive/paused` and
“Sync is paused.” even for stale `state=live`, or fresh
`state=blocked, reason=database_unavailable`. Native replication may still run.
The response still exposes the raw state/freshness; its derived health message is
misleading. The CLI's successful `--wait` path separately checks freshness and
does not reproduce this false success.

**Evidence.** `TestInvestigationPauseAcknowledgement` reproduces both inputs and
the exact misleading health message.

**Code.** [freshness.go](../pkg/datapipeline/freshness.go), `pipelineHealth`,
paused branch around line 62; [pipeline guide](data-pipelines.md), the pause
acknowledgement and source-unreachable behavior.

**Fix target.** Distinguish requested/awaiting pause, failed or unconfirmed pause,
and freshly acknowledged pause. Only claim paused after a current-revision native
acknowledgement. Verify stale observations and destination failures, while keeping
retention warnings visible for a confirmed paused slot.

**Implementation (2026-11-13).** Health reports `pause_pending` or `pause_failed` until the current revision has a fresh paused acknowledgement. Confirmed pause retains WAL warnings. Covered by `TestPipelinePauseHealthRequiresAcknowledgement`.

## PIPE-003 — Paused slots can lose monitoring without an alert

**Trigger and impact.** A paused pipeline retains its upstream replication slot.
If source access fails, the worker can report `paused/source_unavailable` without
WAL evidence. If the worker stops, its observation becomes stale. Neither case
opens a monitoring incident for paused intent: stale detection is restricted to
running pipelines, and a fresh paused observation without retention evidence also
returns no condition. Retention can become exhausted while the user receives no
new warning that the slot's headroom is unknown. This is separate from the false
pause message in PIPE-002: even a correctly acknowledged pause needs monitoring.

**Evidence.** `TestInvestigationPausedMonitoringBlindSpot` gets an empty incident
code for both fresh source-unavailable evidence and an observation ten minutes
old, with no retained-WAL sample.

**Code.** [alerts.go](../pkg/datapipeline/alerts.go), `alertCondition`, around
lines 164–178; [engine.go](../pkg/datapipeline/engine.go), source-connect failure
handling for paused intent.

**Fix target.** Alert on lost monitoring for paused pipelines that still own slots,
with a distinct explanation from stalled apply. Do not warn merely because apply
is intentionally paused. Verify missing source evidence, stopped workers,
recovery, and current retention-pressure warnings without automatically resyncing.

**Implementation (2026-11-13).** Paused pipelines now open `monitoring_stale` incidents for stale observations or source/credential failures. Intentional pause does not create an apply-lag incident. Covered by `TestPipelinePausedRetentionMonitoringRemainsObservable` and the existing alert suite.

## PIPE-004 — Discovery can overwrite evidence needed by saved setup

**Trigger and impact.** A saved setup selects `orders`. Before its check step runs,
another discovery request for the same connection searches `customers` and saves
a fresh passed result. Saved setup sees matching connection revision/database
and fresh capacity, then treats the missing selected table as a terminal
`connection_check_required` blocker. It does not enqueue an exact-table check for
its own selection. Retrying against the same fresh result repeats the blocker;
the user must replace/expire that evidence to recover.

**Evidence.** `TestInvestigationUnrelatedDiscoveryBlocksSetup` uses the real
PostgreSQL store and saved setup worker. Setup becomes
`blocked/connection_check_required`, and the latest check remains the completed
`customers` search rather than a new exact `orders` check.

**Code.** [setup.go](../pkg/datapipeline/setup.go), `setupCheck`, around lines
289–307; [checks_store.go](../pkg/datapipeline/checks_store.go), the shared latest
connection-check record and request selection.

**Fix target.** Include selection compatibility in reusable-check eligibility.
Queue an exact check for saved intent when unrelated evidence replaces it;
preserve the one-job-per-connection and stale-worker fences. Cover search/exact
selection changes and different saved setups sharing a connection. A genuine
unsupported selected table should remain an actionable blocker.

**Implementation (2026-11-13).** Saved setup reuses a check only when it covers its exact selection; an unrelated search queues an exact check. Existing missing/unsupported-table blockers remain. Covered by `TestSetupRefreshesEvidenceForItsSavedSelection` and the setup suite.

## PIPE-005 — CLI resume wait fails on stale previous errors

**Trigger and impact.** A pipeline is blocked on source authentication. After
credentials are corrected, `resume` increments intent revision while preserving
the previous observed state/reason until reconciliation. `resume --wait` receives
that stale blocked observation and immediately exits with an error. The failure
branch in `waitDataPipeline` does not require current observation freshness,
although its success branch does. Automation sees failure without even polling
for the recovery it just requested. The same unchecked branch covers stale
`resync_required` observations.

**Evidence.** `TestInvestigationWaitRejectsStaleFailure` supplies revision 2 with
the revision-1 authentication failure. The next fake poll would return live, but
the wait exits after zero polls. Store control code confirms how resume preserves
the old observation while advancing revision.

**Code.** [commands_pipeline.go](../cmd/gregale/commands_pipeline.go),
`waitDataPipeline`, failure branch around line 298;
[postgres.go](../pkg/datapipeline/postgres.go), `PostgresStore.Control`;
[queries.sql](../pkg/datapipeline/queries.sql), `PipelineControl`.

**Fix target.** Treat only current-request observations as terminal failure;
continue polling old observations under the existing timeout. Cover stale blocked
and resync-required states, a newly observed failure, cancellation, and successful
recovery. Do not weaken genuine terminal deletion handling.

**Implementation (2026-11-13).** CLI wait requires current-revision terminal evidence: transient blocked observations must be fresh, while a durable resync requirement from the same revision remains terminal after its observation ages. Covered by `TestPipelineWaitPollsPastOldFailure`.

## PIPE-006 — Valid digit-led names cannot provision a CLI mirror

**Trigger and impact.** `pipeline create 123-orders ...` passes pipeline slug
validation, performs source checks, then tries to create a database named
`123-orders-mirror`. Managed Postgres names must begin with a lowercase letter,
so the API rejects it. Saved setup already has a naming helper that prefixes
digit-led names; CLI creation builds the name separately.

**Evidence.** `TestInvestigationDigitLedMirrorName` runs the real CLI command
against a local fake API, records the generated invalid name, and observes
failure after one successful source check. It also calls the real pipeline and
database name validators.

**Code.** [commands_pipeline.go](../cmd/gregale/commands_pipeline.go),
`cmdPipelineCreate`, generated mirror request around line 194;
[types.go](../pkg/managedpostgres/types.go), `ValidName`;
[setup.go](../pkg/datapipeline/setup.go), `setupMirrorName`.

**Fix target.** Share a deterministic naming policy between CLI and saved setup,
and validate derived resource names before side effects. Accept valid digit-led
pipeline slugs; preserve retry/idempotency behavior and the full allowed length.

**Implementation (2026-11-13).** CLI and durable setup share a valid mirror-name generator, including a leading prefix for digit-led slugs. Covered by `TestPipelineGeneratedMirrorNamesAcceptDigitLedSlugs`.

## PIPE-007 — Distinct pipeline names generate the same source name

**Trigger and impact.** With `--url-file`, CLI source names are the first 32
characters of the pipeline slug plus `-source`. Two valid names such as 32 `a`
characters followed by `-north` and `-south` produce the same account-unique
source name, even for different source URLs. The second registration conflicts.
Credential-derived idempotency keys do not resolve a collision between distinct
requested resources.

**Evidence.** `TestInvestigationSourceNameCollision` runs both CLI requests with
different fake source URLs and captures identical generated connection names.
The fake API does not enforce uniqueness; the real catalog's
`UNIQUE(account_id,name)` establishes why the second creation conflicts.

**Code.** [commands_pipeline.go](../cmd/gregale/commands_pipeline.go),
`cmdPipelineCreate`, source-name truncation around lines 152–156;
[schema.sql](../pkg/datapipeline/schema.sql), `data_pipeline_connections`.

**Fix target.** Use a bounded readable prefix plus a stable suffix derived from
the full intended resource identity. Verify distinct long names, retried identical
requests, and account-scoped uniqueness without reusing unrelated credentials.

**Implementation (2026-11-13).** Long generated source names retain a digest of the full pipeline slug, with stable retry/idempotency behavior. Covered by `TestPipelineLongSourceNamesAreDistinctAndRetryStable`.

## PIPE-008 — CLI checks optional Auth only after provisioning

**Trigger and impact.** With application Auth enabled, request
`pipeline create ... --api orders-api --auth missing-auth`. Syntax passes, the
mirror database is created, and the pipeline reaches live before the nested
`cmdDataAPICreate` first resolves the Auth app. A missing or invalid Auth app then
fails the command, leaving the new database and replication running even though
the requested end-to-end API setup could have been rejected before provisioning.
The durable setup adapter already checks Auth during planning. These flags are
behind `FAAS_APPLICATION_AUTH_ENABLED`; the ordinary pipeline-only path is not
affected by this specific issue.

**Evidence.** `TestInvestigationAuthPreflightAfterProvisioning` uses the actual
CLI flow with a local fake API. The first Auth lookup occurs after one mirror
creation and one live pipeline creation, then returns 404 and command failure.
It does not provision a real provider database or estimate billing amounts.

**Code.** [commands_pipeline.go](../cmd/gregale/commands_pipeline.go),
`cmdPipelineCreate`, delayed `cmdDataAPICreate` call around line 223;
[commands_data_api.go](../cmd/gregale/commands_data_api.go), Auth resolution;
[data_api_setup.go](../pkg/apid/datapipelines/data_api_setup.go), `DataAPISetup.Plan`.

**Fix target.** Preflight supplied Auth and other cheaply knowable API prerequisites
before creating resources. Prefer the existing durable setup semantics for safe
retry and recovery. Reject a missing Auth app before any source registration,
database, or pipeline mutation; retain explicit recovery for failures that can
only occur after provisioning.

**Implementation (2026-11-13).** Optional Auth resolution and API-name availability checks now precede source registration or mirror provisioning. Covered by `TestPipelineAuthPreflightPrecedesEveryMutation`.

## PIPE-009 — Revoking the final reader requires a policy change

**Observed behavior and impact.** An initialized pipeline with sole reader
`alice` cannot preview an empty reader set. That blocks complete reader
revocation through the access-management workflow. During billing grace a
replacement subject is not an allowed strict reduction either. Pausing preserves
readable rows; disconnecting affects replication, so neither provides an
equivalent access-only action. This finding does not claim that an unauthorized
subject can read data.

**Evidence.** `TestInvestigationLastReaderCannotBeRevoked` gets `ErrInvalid` for
empty readers both with and without deployment entitlement.

**Code and policy.** [readers.go](../pkg/datapipeline/readers.go), `validReaders`
and `Service.Readers`; [schema.sql](../pkg/datapipeline/schema.sql), 1–32 reader
constraint. [ADR-20261103230000](adr/20261103230000-pipeline-safety-controls-and-retirement-refresh-retry.md)
explicitly says “Retain the one-reader minimum.” **The current implementation
matches that decision.**

**Decision/fix target.** Amend the policy before implementation: either permit
zero applied readers on updates, or add an explicit access-disable action that
preserves replication. Keep the existing preview/approval, account, generation,
revision, and private receipt fences. Validate grace-period revocation and direct
API reads with existing tokens after the access change is acknowledged.

**Implementation (2026-11-13).** Reader updates accept an empty set and keep preview/approval, revision fencing and fail-closed RLS; creation still requires at least one subject. Dashboard and output schema agree. Covered by `TestReadersCanRevokeLastSubjectDuringGrace`, the migration regression and native revocation/disconnect test.

## PIPE-010 — Resync confirmation differs between CLI and dashboard

**Observed behavior and impact.** The dashboard requires an explicit
`confirm_resync=yes` checkbox before replacing imported rows. CLI
`pipeline resync ID` directly submits the control request, including under
`--non-interactive --json`, with no confirmation flag or warning. Other CLI
commands have an automation confirmation helper, but this command never calls it.
The verb is itself explicit intent and the documented CLI example uses it this
way; classify this as a safety/UX consistency decision, not an authorization
bypass or a claim that current CLI behavior violates a universal confirmation rule.

**Evidence.** `TestInvestigationResyncSkipsConfirmation` runs the real CLI with
non-interactive JSON output and no approval flag. It successfully sends one
`resync` control mutation to a local fake API.

**Code.** [commands_pipeline.go](../cmd/gregale/commands_pipeline.go),
`cmdPipelineControl`; [dashboard.go](../pkg/apid/datapipelines/dashboard.go),
control confirmation around lines 171–175;
[automation_mode.go](../cmd/gregale/automation_mode.go),
`requireAutomationConfirmation`.

**Decision/fix target.** Choose and document a consistent destructive-action
contract. If adding explicit CLI confirmation, expose a scriptable flag and
reject unconfirmed automation before any mutation. Explain full recopy and read
interruption, update generated CLI documentation, and cover confirmed automation,
interactive decline, and retained-row disconnect separately.

**Implementation (2026-11-13).** Interactive CLI resync requires the typed phrase `replace imported tables`; automation/JSON requires `--yes` before any API call. Covered by `TestPipelineResyncRequiresExplicitAutomationConfirmation`.

## PIPE-011 — Disconnect strands approved reader revocations

**Trigger and impact.** Approve reducing readers from `[alice,bob]` to `[bob]`,
then disconnect before the worker applies that change. Delete intent is accepted,
but `applyReaders` explicitly skips deleted intent. Teardown retains the imported
rows and their old RLS policy. The catalog can reach terminal `deleted` while the
reader change remains `approved`, with `alice` still able to read retained rows.
Deleted pipelines are no longer reconciled, so the approved access change never
finishes. This concerns a pending approved change, not a revocation that was
already acknowledged as applied; disconnect is not itself an access-revocation
action.

**Evidence.** `TestInvestigationRound2DisconnectSkipsApprovedReaderRevocation`
uses the real preview/approval/control store, engine teardown, terminal catalog
acknowledgement, and RLS SELECT under a restricted role. It observes one retained
row for `alice` after terminal deletion. The remote fixture starts with no native
subscription/publication remaining; it proves the skipped access change and
retained policy, not a complete native replication teardown under load.

**Code.** [readers_engine.go](../pkg/datapipeline/readers_engine.go),
`applyReaders` line 47;
[engine.go](../pkg/datapipeline/engine.go), `Engine.Reconcile` and
`cleanupGeneration`'s retained-data deletion branch;
[queries.sql](../pkg/datapipeline/queries.sql), `PipelineControl`, `PipelineFinish`,
and the terminal-row exclusion in `PipelineClaim`.

**Fix target.** Define and enforce the disposition of approved reader changes
before terminal disconnect. Prefer finishing approved reductions under the
existing private receipt and revision fences without requiring source access;
handle pending grants separately rather than silently retaining an approval
forever. Test approval→disconnect before a worker tick, lost acknowledgements,
unavailable source keys, and actual retained-row reads after native teardown.

**Implementation (2026-11-13).** Disconnect applies and acknowledges approved reader reductions before cleanup. Pending grants prevent disconnect until reconciled. Revocation remains source-independent and available during grace. Covered by `TestNativeFinalReaderRevocationSurvivesDisconnect` and the reader tests.

## PIPE-012 — Verification is uncancellable before the worker creates a run

**Trigger and impact.** A manual verification request initially persists only a
`ManualAttempt`, with no run ID or running/waiting progress state. Cancellation
requires the worker-generated run ID and a running/waiting state. If the source
key is unavailable before the worker starts a proof, the pipeline remains blocked
with `verification=pending` and `verify_requested=true`, but there is no accepted
cancel request for that queued intent. A fresh verification request also conflicts.

There is an additional entry into the same stuck state: `pause` preserves the last
observed `state=live` until reconciliation, and `Control(verify)` checks that
observed state without requiring running intent. A verify request immediately
after pause is accepted with desired state still paused; the paused worker path
does not start a proof, and cancellation also rejects paused intent.

**Evidence.** `TestInvestigationRound2QueuedVerificationCannotBeCancelledWithoutSource`
runs real catalog controls, the engine's unavailable-key safety path, and a worker
finish. Pending verification has no run ID; cancellation and a new verification
both conflict. `TestInvestigationRound2VerifyAcceptedAfterPauseRequest` separately
reproduces pause→verify acceptance and rejection of cancellation after a simulated
ordinary paused observation. Neither probe performs a native verification scan.

**Code.** [postgres.go](../pkg/datapipeline/postgres.go), `Control` lines 303–308;
[queries.sql](../pkg/datapipeline/queries.sql), `PipelineControl` seeds
`ManualAttempt`, while `PipelineCancelVerification` requires running intent,
matching `ID`, and running/waiting state;
[verification.go](../pkg/datapipeline/verification.go), `CancelVerification`;
[engine.go](../pkg/datapipeline/engine.go), `pipelineObservation`,
`reconcileUnavailable`, and the paused reconciliation branch.

**Fix target.** Give queued verification an identity that can be cancelled before
any remote work, preserving request/revision/account fencing and grace-period
eligibility. Reject verification on paused/deleting intent or explicitly support
queued semantics with cancellation. Cover cancellation before the first tick,
unavailable source keys, source connection failure, pause races, and no automatic
restart of cancelled verification when connectivity returns.

**Implementation (2026-11-13).** Manual verification gets a stable queued request identity immediately, supports cancellation before source work and after pause intent, and rejects new verification while paused. Physical proofs with lost acknowledgements are retired by attempt as well as identity. Legacy queue metadata is upgraded in bounded, committed batches. Covered by `TestQueuedVerificationCanBeCancelledWithoutSourceAndDuringPause`, `TestPipelineFindingsMigrationUpgradesLegacyQueuedRequests` and the verification suite.

## PIPE-013 — Hook installation after API startup never enables the read gate

**Trigger and impact.** A read-only Data API starts before pipeline hooks exist.
`configurePipelineReadGate` returns false and startup proceeds without a pipeline
pre-request hook or coordination claim. Installing the hooks and valid grants
later does not update that running configuration. This can occur with an API
that predates pipeline setup, or startup between mirror-table creation and the
subsequent hook-install transaction. It also matters when upgrading workers and
runtimes independently. The new 503 availability contract remains inactive until
the runtime is restarted/reconfigured; reloading only the database schema cache
does not create the runtime's trusted coordination claim or hook environment.

**Evidence.** `TestInvestigationRound2LateHookInstallationNeedsRuntimeRestart`
runs the actual Node configuration loader against a disposable PostgreSQL
database, then installs the real hook SQL and valid grants. The actual gateway
still forwards a request without pipeline coordination and leaves its hook
environment unset. Explicit reconfiguration detects valid hooks and enables
them. Its upstream is a local fake HTTP server; this is a configuration/admission
reproduction, not an end-to-end PostgREST gated-read assertion. This differs from
PIPE-001: the required privileges are already valid here.

**Code.** [pipeline-read-gate.mjs](../cmd/gregale/templates/data-api/pipeline-read-gate.mjs),
missing-hook return at line 22;
[server.mjs](../cmd/gregale/templates/data-api/server.mjs), one-time startup
configuration at line 395 and conditional trusted-claim signing;
[engine.go](../pkg/datapipeline/engine.go), `prepareMirror` commits tables before
installing helpers and does not request an initial runtime refresh.

**Fix target.** Make first hook installation a reliable serving-configuration
boundary, including for already running/snapshotted APIs. Use the existing durable
refresh/freshness mechanism or a safe installation/configuration contract that
also supports ordinary non-pipeline read-only databases. Test API-before-pipeline,
the initial-install startup window, and snapshot restores, then verify real gated
reads return 503 without requiring a manual redeploy.

**Implementation (2026-11-13).** Hook installation enqueues the existing durable runtime refresh and invalidates snapshots; a separate-purpose receipt prevents duplicate restarts or schema-pruning interference. Pause/revocation safety actions take precedence. Covered by `TestReadGateRefreshIsDurableAndIndependentOfSchemaRefresh` and `TestNativeReadHooksRefreshAnAPIThatStartedBeforeInstallation` with signed HTTP reads.

## PIPE-014 — Verification ignores source primary-key column order

**Trigger and impact.** A supported table has physical columns
`(tenant,sequence,payload)` with `PRIMARY KEY(sequence,tenant)`. Inspection records
only a boolean key-membership flag and returns columns in physical order.
`primaryColumns` therefore builds verification bounds/order as `(tenant,sequence)`.
The source's existing primary-key index cannot supply that order. Each bounded
verification chunk can scan and sort the remaining source table, despite returning
only about 2,000 rows. The source SHARE lock prevents writes during that work;
large tables can repeatedly exhaust query budgets without advancing a proof.
This is unnecessary source work, not evidence of incorrect row comparisons.

**Evidence.** `TestInvestigationRound2VerificationDiscardsPrimaryKeyOrder` inspects
a supported 100,000-row table and runs the generated query through PostgreSQL 16
`EXPLAIN (ANALYZE,BUFFERS)`. The current ordering uses a sequential scan and source
key sort. A query control aligned with the existing primary-key order uses that
index; small bookkeeping sorts over the bounded result remain. The recorded local
run took approximately 50 ms versus 17 ms. Those times are fixture observations,
not production latency guarantees or a reproduced timeout.

**Code.** [source_inspection.go](../pkg/datapipeline/source_inspection.go),
`inspectSourceTable` records membership and orders by `attnum` around lines 85–92;
[verification_receipt.go](../pkg/datapipeline/verification_receipt.go),
`primaryColumns`; [verification_scan.go](../pkg/datapipeline/verification_scan.go),
`verificationQuery` and `lockVerificationSource`.

**Fix target.** Preserve usable primary-key ordering in inspection and use a
consistent key order across mirror creation, range bounds, triggers, and scans.
Version/fence existing proof identities if persisted ordering changes. Validate
reverse/composite keys and indexes with INCLUDE columns. Preserve deliberate
collation/canonical-value requirements; this issue does not authorize arbitrary
index installation or removing verification locks.

**Implementation (2026-11-13).** Verification hydrates primary-key positions, excludes INCLUDE columns, and uses source key order for bounds/scans/new mirrors. Planner metadata stays outside approval hashes; a versioned physical proof fingerprint includes database and query key order, including DDL between metadata reads. Covered by `TestVerificationUsesSourcePrimaryKeyOrder` and the native verification suite.

## PIPE-015 — CLI source lookup rejects valid equivalent UUIDs

**Trigger and impact.** Supply an uppercase spelling of an owned connection UUID
to `pipeline connection tables/check`, creation with `--connection`, or rotation.
The CLI compares raw input with the lowercase IDs returned by the connection
list, reports source-not-found, and never sends the intended operation. UUID
validation and the server accept these equivalent spellings, so this is a client
compatibility inconsistency. Canonical IDs copied directly from CLI output work.

**Evidence.** `TestInvestigationRound2CLIRejectsEquivalentConnectionUUID` parses
the uppercase UUID successfully, serves its canonical owned connection through a
local fake API, and runs the actual `connection tables` command. It exits 1 before
issuing any check. Creation and rotation use the same raw-comparison pattern and
were inspected, not independently executed in this probe.

**Code.** [commands_pipeline_checks.go](../cmd/gregale/commands_pipeline_checks.go),
`requestConnectionCheck` line 26;
[commands_pipeline.go](../cmd/gregale/commands_pipeline.go), rotation around line
98 and creation around line 169. The server's identity contract is described in
[ADR-20261103225803](adr/20261103225803-pipeline-cancellation-readiness-and-uuid-identity.md).

**Fix target.** Parse and canonicalize connection IDs once at CLI boundaries before
lookup, idempotency-key construction, and requests. Cover uppercase, compact,
braced, and URN forms accepted by the existing parser, retaining clear failures
for malformed and genuinely foreign IDs. Do not normalize case-sensitive table
names or reader subjects along with UUIDs.

**Implementation (2026-11-13).** CLI canonicalizes accepted UUID spellings before owned-source lookup, requests and idempotency-key construction. Covered by `TestPipelineConnectionLookupCanonicalizesUUIDs`.

## PIPE-016 — Initial-copy stalls are absent from health alerting

**Trigger and impact.** Native initial copy cannot progress while the worker can
still inspect metadata—for example, the destination's connection to the source
is blocked while the worker's connection succeeds. Ordinary initial reconciliation
reports `copying` whenever not all subscription relations are ready, without
persisting per-table progress age or inspecting native sync-error counters.
The five-minute stall/high-water logic is used for later table additions/repairs,
not initial copy. With current worker observations and low WAL pressure, health
and alerts cannot distinguish hours of no initial-copy progress from a healthy
copy. A small or idle source need never reach a separate retention warning.

**Evidence.** `TestInvestigationRound2InitialCopyHasNoStallAlert` passes a
24-hour-old, freshly monitored pipeline with 0/1 tables ready and no WAL pressure
through the actual health/incident functions. It yields `pending/initial_copy`
and no incident. Source inspection confirms no initial-copy progress-age path.
The probe models that durable state; it does not run a 24-hour test or reproduce
the proposed network failure with native replication.

**Code.** [engine.go](../pkg/datapipeline/engine.go), initial readiness branch
around lines 343–350;
[table_copy.go](../pkg/datapipeline/table_copy.go), `copyProgress` and
`observeTableCopies` require the later schema/table workflow;
[alerts.go](../pkg/datapipeline/alerts.go), `alertCondition`;
[freshness.go](../pkg/datapipeline/freshness.go), the initial-copy health branch.

**Fix target.** Extend durable, bounded copy-progress monitoring to the initial
selection and alert on lack of observed progress or persistent native copy
errors. Use progress age rather than total pipeline age so a large advancing copy
does not generate false alarms. Test worker restarts, retrying native COPY,
unknown counters, idle sources, and failed destination-to-source connectivity.
Keep recovery explicit; a stall must not trigger automatic destructive resync.

**Implementation (2026-11-13).** Initial copy now reuses durable per-table high-water/progress clocks and emits `initial_copy_stalled` health plus a `sync_blocked` incident. Worker restarts preserve progress age; recovery remains explicit. Covered by `TestPipelineInitialCopyStallOpensIncident` and `TestNativeInitialCopyStallPersistsAndRecovers`.

## PIPE-017 — An empty tenant membership set prevents later grants

**Status:** fixed during the CUST-007–009 batch; native regression coverage retained.

A complete tenant revocation wrote the literal `false` policy while keeping the
mirror's shared readable receipt true. A later approved membership change treated
that policy as quarantine and skipped it; ordinary reconciliation also skipped
reopening because the shared receipt already said readable. The catalog could
therefore report the new membership as applied while SQL still returned no rows.

The empty-reader predicate now remains distinct from quarantine's literal deny
policy. A later approved grant updates the empty-reader policy, while actual
quarantine stays closed. Connecting or replacing an Auth mapping deliberately
closes the shared gate first, covering older empty-reader receipts as well as
lost acknowledgements. `TestNativeTenantRegrantAfterEmptyMembershipPreservesQuarantine`
and `TestNativeEvolutionAuthProofExpiryRevocationAndUnavailableSource` cover both
manual and synchronized access. This is a defect in the previously implemented
tenant-serving baseline, not a claim about production customer impact.

## PIPE-018 — An automatic membership approval can race owner controls

**Status:** fixed during the CUST-007–009 batch; catalog regression retained.

Automatic Auth reader approvals are staged under the worker lease without
incrementing the owner's configuration revision. A control request could read
an applied reader set, then commit deletion after a new grant was staged at that
same revision. The subsequent worker correctly refuses to apply a grant during
deletion, leaving the pending change blocking cleanup.

The control UPDATE now compares the reader-change ID observed by its initial
read, as well as the owner revision. A concurrent snapshot causes an actionable
conflict instead of accepting stale deletion. The catalog regression in
`TestAuthReaderProofIsDurableAndCannotRenewInGrace` interleaves the real staging
and control queries and proves that the stale control is rejected. This was
found in the new local Auth synchronization implementation; no production impact
is claimed.

## Customer roadmap

Reviewed 2026-11-14 against the evolution branch. CUST-007–012 have approved
implementations summarized above; the bounded scope and remaining qualification
are explicit. These are product opportunities, not reproduced baseline defects.
Priorities are code-review recommendations, not measured customer-demand rankings.

| ID | Customer problem and example | Bounded first release |
| --- | --- | --- |
| CUST-007 | Customers cannot evolve an explicit projection without creating a newly reviewed pipeline: a portal needs `order_total`, or another selected table. | Preview and approve additions to columns/table selections; preserve primary and tenant keys; backfill and verify affected tables; refresh API contracts before exposing new fields. Avoid a whole-pipeline rebuild. Treat removals and incompatible type changes separately. |
| CUST-008 | B2B applications must maintain subject/tenant lists manually as employees join or leave customer organizations. | Bind an owner-approved Auth app and organization-to-tenant mapping; synchronize authoritative memberships with durable deduplication and periodic reconciliation. Give revocation priority and report synchronization age. JWT organization claims alone must not grant access. |
| CUST-009 | A customer portal needs orders with customers/items, but mirrored foreign-key relationships are absent. | Owner-approved relationships and simple read views using existing Data API relationship/type export. Preserve RLS on every dependency and require all dependencies to be ready. Start with declared relationships and simple views; defer arbitrary transformations and materialized aggregates. |
| CUST-010 | Reporting customers need hourly/nightly data without maintaining continuous replication or keeping Neon compute awake between runs. Some customers can supply only read credentials. | A distinct scheduled snapshot mode for selected tables, qualified separately with suitable read-only credentials. Stage and verify a complete snapshot before activation, retain the last successful version after failures, and expose capture time/staleness. Release source sessions and avoid retaining a CDC slot between runs; source-provider sleep/billing remains provider-dependent. |
| CUST-011 | A mirror can match its source while still containing invalid business data, such as negative inventory or orphaned orders. | Scheduled, bounded checks on the mirror using curated rules or reviewed assertions, with count/status reports and alerts. Reuse Data API verification concepts. Do not export failing row values or automatically trigger destructive recovery. |
| CUST-012 | Customers need predictable source impact and support for growing mirrors. Existing verification and index limits are fixed pilot bounds. | Add verification/repair schedules and configurable work budgets, explain progress/estimate uncertainty, and qualify a separate resumable path for larger approved index builds. Native PostgreSQL COPY must not be advertised as having a hard throughput cap without a mechanism that actually enforces it. |

Baseline evidence and reused components:

- [The serving contract](../pkg/api/data_pipeline_serving.go) originally declared columns
  immutable. [Table-selection changes](../pkg/datapipeline/tables.go) originally rejected
  pipelines with an explicit projection. Ordinary additive schema handling and
  scoped repair already exist; CUST-007 extends those workflows rather than
  replacing them.
- [Tenant reader changes](../pkg/datapipeline/tenant_readers.go) accept explicit
  owner-managed membership arrays. Auth already records organization membership
  changes in [its audit log](../cmd/gregale/templates/auth/sql/0020_org_audit_log.sql),
  providing an integration starting point; the new bridge reconciles authoritative
  snapshots instead of depending on audit-event delivery.
- [Mirror table creation](../pkg/datapipeline/tables_receipt.go) creates selected
  columns, primary keys and RLS, without source foreign keys. The Data API already
  supports [typed relationships](data-api.md#typed-relationships), computed
  relationships and views that respect caller permissions.
- [Current source requirements](data-pipelines.md#source-and-destination-requirements) require
  a writable source and logical-replication privileges; the Neon onboarding
  section documents continuous-subscriber compute implications. Scheduled mode
  now has its own serving, recovery and qualification contract in the linked ADR.
- [Verification](../pkg/datapipeline/verification_scan.go) proves bounded source/
  mirror consistency. Existing [Data API assertions](data-api.md#verify-reusable-data-integrity-checks)
  and the surrounding verification documentation are building blocks for
  business-rule checks; scheduled pipeline quality monitoring extends them with
  bounded coverage and count-only incident reporting.
- [Pipeline limits](../pkg/api/limits.go) currently bound verification chunks and
  index builds. Scheduling and larger-workload support should preserve explicit
  source impact, storage and approval controls.

Before customer rollout, finish the current PRs' CI/review and the real Neon
source qualification described in CUST-003. The current implementations remain
in the approved staging pilot; this roadmap does not change rollout gates.

## Related improvement backlog

Keep these enhancement tracks alongside the concrete findings; their absence
alone is not a reproduced bug:

- Durable CLI setup and preflight are implemented in CUST-005, including retained
  resource IDs and resumable API deployment (related to PIPE-006–008).
- Expose advanced table/schema/reader recovery workflows consistently across
  dashboard, API, CLI, and SDK surfaces.
- Add an explicit reviewed Auth detach/zero-mapping workflow; this release
  requires at least one organization mapping while synchronization is connected.
- Make verification cost and source write-lock/load impact clearer and more
  controllable within the existing bounded verification model.
- Tenant access beyond trusted whole-dataset subjects is implemented in CUST-001,
  with fail-closed RLS and explicit membership approval (related to PIPE-009).
- Destination index/query advice with explicit storage and replication-write
  tradeoffs is implemented in CUST-006; larger concurrent builds remain future work.

## Reproduction and follow-up

The [evidence directory](ops/evidence/20261112-pipeline-investigation/README.md)
contains ten isolated probes, the runner, and captured output. They deliberately
assert the observed bad behavior: **a passing probe means the finding was
reproduced**, not that the feature is correct. They are not normal test-suite
regressions and must be inverted/adapted when implementing a fix.

The [second-pass evidence](ops/evidence/20261112-pipeline-investigation-round2/README.md)
adds seven probes for PIPE-011–016, including real RLS reads, query plans, and the
Node runtime's hook configuration. Its README records the reproduction limits.

The PostgreSQL probes use disposable test databases; CLI probes use local fake
HTTP servers. No production account, source database, webhook destination, or
provider mutation was used. Native replication/provider rollout tests were not
rerun during the original documentation-only investigation. Remediation adds
normal regression and local native acceptance tests; the historical probes still
assert defective behavior and must not be treated as acceptance tests. Production
and live-provider rollout qualification remain separate.

Remediation test sources: [worker and catalog regressions](../pkg/datapipeline/findings_regression_test.go),
[native acceptance](../pkg/datapipeline/findings_native_test.go),
[credential integration](../pkg/managedpostgres/neon/pipeline_read_gate_test.go), and
[CLI regressions](../cmd/gregale/commands_pipeline_findings_test.go).

The ownership-conflict regression caught during qualification was corrected by
placing paused hook maintenance after the existing ownership checks and native
suspension. `TestNativeRetiredGenerationOwnershipConflict` and the related pause,
retirement, cancellation and recovery scenarios passed in the final race-enabled
worker run and the complete final native acceptance run. The qualification record
includes the clean full-run result and the earlier failure for historical context.
