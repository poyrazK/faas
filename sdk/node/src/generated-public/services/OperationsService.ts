/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowActionPreviewRequest } from '../models/OperationWorkflowActionPreviewRequest.js';
import type { OperationWorkflowActionPreviewResponse } from '../models/OperationWorkflowActionPreviewResponse.js';
import type { OperationWorkflowAttentionResponse } from '../models/OperationWorkflowAttentionResponse.js';
import type { OperationWorkflowAttentionSummary } from '../models/OperationWorkflowAttentionSummary.js';
import type { OperationWorkflowCase } from '../models/OperationWorkflowCase.js';
import type { OperationWorkflowCoordinationTimeline } from '../models/OperationWorkflowCoordinationTimeline.js';
import type { OperationWorkflowOutcomesResponse } from '../models/OperationWorkflowOutcomesResponse.js';
import type { OperationWorkflowOutcomeSummary } from '../models/OperationWorkflowOutcomeSummary.js';
import type { OperationWorkflowPerformanceInstancesResponse } from '../models/OperationWorkflowPerformanceInstancesResponse.js';
import type { OperationWorkflowPerformanceSummary } from '../models/OperationWorkflowPerformanceSummary.js';
import type { OperationWorkflowPlaybookEvidenceWatch } from '../models/OperationWorkflowPlaybookEvidenceWatch.js';
import type { OperationWorkflowPlaybookEvidenceWatchRequest } from '../models/OperationWorkflowPlaybookEvidenceWatchRequest.js';
import type { OperationWorkflowPlaybookProgress } from '../models/OperationWorkflowPlaybookProgress.js';
import type { OperationWorkflowPlaybookProgressStartRequest } from '../models/OperationWorkflowPlaybookProgressStartRequest.js';
import type { OperationWorkflowPlaybookProgressUpdateRequest } from '../models/OperationWorkflowPlaybookProgressUpdateRequest.js';
import type { OperationWorkflowPlaybookReviewRequest } from '../models/OperationWorkflowPlaybookReviewRequest.js';
import type { OperationWorkflowReadinessComparisonRequest } from '../models/OperationWorkflowReadinessComparisonRequest.js';
import type { OperationWorkflowReadinessComparisonResponse } from '../models/OperationWorkflowReadinessComparisonResponse.js';
import type { OperationWorkflowReadinessCoordinationRequest } from '../models/OperationWorkflowReadinessCoordinationRequest.js';
import type { OperationWorkflowReadinessFollowUpsResponse } from '../models/OperationWorkflowReadinessFollowUpsResponse.js';
import type { OperationWorkflowReadinessInboxResponse } from '../models/OperationWorkflowReadinessInboxResponse.js';
import type { OperationWorkflowReadinessNotification } from '../models/OperationWorkflowReadinessNotification.js';
import type { OperationWorkflowReadinessNotificationCheck } from '../models/OperationWorkflowReadinessNotificationCheck.js';
import type { OperationWorkflowReadinessQueueResponse } from '../models/OperationWorkflowReadinessQueueResponse.js';
import type { OperationWorkflowReadinessRequest } from '../models/OperationWorkflowReadinessRequest.js';
import type { OperationWorkflowReadinessResponse } from '../models/OperationWorkflowReadinessResponse.js';
import type { OperationWorkflowReadinessSubscription } from '../models/OperationWorkflowReadinessSubscription.js';
import type { OperationWorkflowReadinessSubscriptionRequest } from '../models/OperationWorkflowReadinessSubscriptionRequest.js';
import type { OperationWorkflowReadinessSubscriptionsResponse } from '../models/OperationWorkflowReadinessSubscriptionsResponse.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class OperationsService {
  /**
   * List completed workflows with explicit business outcomes.
   * Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment are required. Only latest retained terminal snapshots with reported outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals cover all matching instances independently of group pagination; cursors are separate from the attention queue. Reports and retention may change during browsing.
   * @returns OperationWorkflowOutcomesResponse A bounded page ordered by latest report publication time descending.
   * @throws ApiError
   */
  public static listAccountWorkflowOutcomes({
    slug,
    scope,
    workflow,
    code,
    tenantId,
    limit = 20,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * For the account outcome queue, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account outcome queue, restrict terminal results to this explicit business outcome code.
     */
    code?: string,
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowOutcomesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-outcomes',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'code': code,
        'tenant_id': tenantId,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List ranked contributors to a workflow performance group.
   * Requires account read scope and MFA. Optional tenant selection stays within account ownership. Explicit app/environment/workflow and cohort/dimension selection are required. Uses the same latest 100 instances per cohort and complete-history eligibility as the performance summary. State groups require state and contract_version; blocker groups require operation/code/contract_version and exactly one of owner or unassigned=true; verification_owner groups require exactly one of owner or unassigned=true. Overall dimensions reject group selectors. An optional cohort_token binds the evaluation time and retained evidence to a prior summary; changed or expired cohorts return 409 and require refreshing the summary. Without a token this evaluates a fresh cohort. Returns every matching eligible contributor up to 100 sorted by observed duration descending with stable identity ties. No cursor is supported. Current blockers can differ from historical contributing groups; pending verification previews are bounded independently of exact counts. Decision dimensions require completed cohort and cohort_token, an exact kind/contract_version/outcome_code and decision_code/rule_id/rule_version for decision groups. Path groups additionally require group_id/path_id; the decision triple is either fully supplied or fully omitted. The exact group must be present in the bounded decision_outcomes summary. Decision dimensions reject state/blocker/owner selectors. Returns the earliest qualifying report and optional business decision fact per contributor; reports after the first terminal revision do not qualify. Condition dimensions also require completed cohort and cohort_token and an exact returned condition_outcomes group selected by contract_version and decision_code/rule_id/rule_version and condition_id/condition_description/condition_result and outcome_code. They reject kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed results can produce overlapping groups. The earliest qualifying fact and all results observed for that exact identity are returned. Missing conditions are not classified as unknown. Active condition dimensions require ongoing cohort and cohort_token, an exact returned active_conditions identity selected by contract_version and decision_code/rule_id/rule_version and condition_id/condition_description/condition_result. Outcome and kind/path/state/blocker/owner selectors are rejected. They use latest explicit decision snapshots in the current contract, return evidence age at the bound evaluation time and measure whole-workflow elapsed or blocked duration. Metadata-only reports preserve evidence; explicit condition omission clears prior results. The optional condition_evidence_status selector restricts active contributors to within_budget, overdue or no_budget. Status counters partition each returned group and the chosen status must have contributors. Budgets come from the reporting Operation pinned definition; deadlines measure report age, not business verification age. The condition_refresh_owner dimension requires ongoing cohort and cohort_token and an exact returned active_conditions.refresh_owners group selected by exactly one of owner or unassigned=true. It rejects contract/state/blocker/condition/result/status and decision/path/outcome selectors. Contributors are ranked by whole-workflow blocked duration and include bounded overdue condition facts with pinned refresh guidance; counts remain exact beyond the preview limit. The condition_refresh_age and condition_refresh_blocked dimensions require ongoing cohort and cohort_token and reject all group selectors. Each returns eligible workflows with at least one overdue selected condition once, across all refresh owners. The former ranks by oldest overdue report age in whole seconds, the latter by full workflow blocked duration; stable workflow identity breaks ties. These overall rankings remain available with zero contributors and are independent of condition and owner display caps. Each contributor includes refresh_priority with the oldest qualifying fact, complete assigned owner list, exact unassigned count and explanation, plus the bounded condition preview. Age ranking does not mean most past deadline or highest business severity. Conflicting snapshots and incomplete histories are excluded. The condition_missing_owner dimension requires ongoing cohort and cohort_token and an exact returned condition_coverage.missing_groups row selected by contract_version, current state and exactly one of owner or unassigned=true. All condition/result/status, decision/path/outcome and blocker selectors are rejected. Counts and eligibility use full condition coverage before previews. Each workflow appears once in its selected group and ranks by full observed workflow blocked duration with stable identity ties. Missing evidence has no report timestamp and is not failed verification; expectations come from the latest reporting Operation pinned definition. Missing condition entries and pinned guidance are previewed up to 16 with exact counts and explicit truncation. The condition_gap_owner dimension requires ongoing cohort, cohort_token and baseline_token bound to the same scope. The baseline must not be later than the current token evaluation. Select an exact returned new_missing_progress.groups row with contract_version, current state, gap_status and exactly one of owner or unassigned=true. All other selectors are rejected; dimensions outside condition_gap_owner and condition_gap_queue reject baseline_token and gap_status. Counts and grouping precede previews; each contributor includes up to 16 matching gap_progress changes with exact counts, prior evidence and pinned guidance. Current report comparisons permit contract boundaries while baseline history must be continuous within its contract; ranking uses full observed report-only blocked intervals without changing eligibility for existing duration groups. Generic duration coverage remains separate from new_missing_progress coverage. The condition_gap_queue dimension requires ongoing cohort and both cohort_token and baseline_token with the same comparison scope and baseline time rules. It returns each eligible workflow once, independent of displayed group caps. Optional gap_status and exactly one of owner or unassigned=true filter matching current gaps; both may be omitted. Contract/state/blocker/condition/decision/path/outcome selectors are rejected. Empty results are valid. Each contributor includes gap_queue_priority with all category counts, sorted assigned owners, exact unassigned count, all pinned missing expectation guidance and a ranking explanation before filters or previews. gap_progress counts and previews contain only filtered gaps. Ranking measures observed report-only blocked intervals, omitting intervals spanning contract boundaries, with stable workflow identity ties.
   * @returns OperationWorkflowPerformanceInstancesResponse Ranked complete-history contributors and exact selected group distribution.
   * @throws ApiError
   */
  public static listAccountWorkflowPerformanceInstances({
    slug,
    scope,
    workflow,
    cohort,
    dimension,
    tenantId,
    baselineToken,
    gapStatus,
    contractVersion,
    state,
    operation,
    code,
    owner,
    unassigned,
    cohortToken,
    kind,
    decisionCode,
    ruleId,
    ruleVersion,
    groupId,
    pathId,
    conditionId,
    conditionDescription,
    conditionEvidenceStatus,
    conditionResult,
    outcomeCode,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * Declared business workflow to investigate.
     */
    workflow: string,
    /**
     * Completed or ongoing cohort.
     */
    cohort: 'completed' | 'ongoing',
    /**
     * Overall measure or exact breakdown dimension.
     */
    dimension: 'state_time' | 'blocked_time' | 'verification_wait' | 'state' | 'blocker' | 'verification_owner' | 'decision_completion' | 'decision_blocked' | 'condition_completion' | 'condition_blocked' | 'active_condition_elapsed' | 'active_condition_blocked' | 'condition_refresh_owner' | 'condition_refresh_age' | 'condition_refresh_blocked' | 'condition_missing_owner' | 'condition_gap_owner' | 'condition_gap_queue',
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Saved account performance baseline; required for condition_gap_owner and condition_gap_queue; rejected by other dimensions.
     */
    baselineToken?: string,
    /**
     * Exact account returned reporting-gap category; required for condition_gap_owner, optional for condition_gap_queue.
     */
    gapStatus?: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed',
    /**
     * Required for state and blocker dimensions.
     */
    contractVersion?: number,
    /**
     * Exact contributing state. Required for the state dimension.
     */
    state?: string,
    /**
     * Exact blocker target Operation. Required for the blocker dimension.
     */
    operation?: string,
    /**
     * Exact blocker code. Required for the blocker dimension.
     */
    code?: string,
    /**
     * Exact public owner. Mutually exclusive with unassigned.
     */
    owner?: string,
    /**
     * Select the empty owner group. Mutually exclusive with owner.
     */
    unassigned?: boolean,
    /**
     * Opaque selector-bound cohort token from the summary or a previous contributor response.
     */
    cohortToken?: string,
    /**
     * Decision outcome group kind.
     */
    kind?: 'decision' | 'path',
    /**
     * Exact reported decision code for decision outcome dimensions.
     */
    decisionCode?: string,
    /**
     * Exact rule ID for decision outcome dimensions.
     */
    ruleId?: string,
    /**
     * Exact rule version for decision outcome dimensions.
     */
    ruleVersion?: string,
    /**
     * Exact selected prerequisite group for path outcome dimensions.
     */
    groupId?: string,
    /**
     * Exact selected path for path outcome dimensions.
     */
    pathId?: string,
    /**
     * Exact reported condition ID for account condition outcome dimensions.
     */
    conditionId?: string,
    /**
     * Exact public condition description for the account returned condition group; UTF-8 byte bound without controls.
     */
    conditionDescription?: string,
    /**
     * Account active-condition report freshness filter. Only ongoing active condition dimensions accept it; the selected status must have contributors in the returned group.
     */
    conditionEvidenceStatus?: 'within_budget' | 'overdue' | 'no_budget',
    /**
     * Exact observed condition result for the account condition outcome group.
     */
    conditionResult?: 'pass' | 'fail' | 'unknown',
    /**
     * Exact latest reported outcome for decision outcome dimensions.
     */
    outcomeCode?: string,
  }): CancelablePromise<OperationWorkflowPerformanceInstancesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-performance/instances',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'tenant_id': tenantId,
        'cohort': cohort,
        'dimension': dimension,
        'baseline_token': baselineToken,
        'gap_status': gapStatus,
        'contract_version': contractVersion,
        'state': state,
        'operation': operation,
        'code': code,
        'owner': owner,
        'unassigned': unassigned,
        'cohort_token': cohortToken,
        'kind': kind,
        'decision_code': decisionCode,
        'rule_id': ruleId,
        'rule_version': ruleVersion,
        'group_id': groupId,
        'path_id': pathId,
        'condition_id': conditionId,
        'condition_description': conditionDescription,
        'condition_evidence_status': conditionEvidenceStatus,
        'condition_result': conditionResult,
        'outcome_code': outcomeCode,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The retained cohort changed. Refresh the summary and use its new cohort_token.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Summarize business workflow performance across retained instances.
   * Requires account read scope and MFA. Optional customer selection stays within account ownership. Explicit app/environment/workflow selection is required. The latest 100 completed and 100 ongoing retained instances form separate cohorts. Matching counts include all retained instances. Incomplete histories are excluded from durations and nearest-rank p50/p95 percentiles with explicit coverage reasons. Each instance contributes one accumulated duration per reported group. At most 1024 reports per instance and 32 output groups per dimension; group truncation does not change cohort totals. Ongoing durations run through evaluation time. No cursor or time-window filter is supported. Retention and newly published reports can change results.
   * @returns OperationWorkflowPerformanceSummary Separate completed and ongoing cohorts with coverage and ranked durations.
   * @throws ApiError
   */
  public static summarizeAccountWorkflowPerformance({
    slug,
    scope,
    workflow,
    tenantId,
    baselineToken,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * Declared workflow name to compare across instances.
     */
    workflow: string,
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Account optional saved performance token anchoring refresh progress by evaluation time and exact query scope. Changed evidence is expected; the old fingerprint is not validated. Progress reconstructs visible reports in current completed and ongoing samples, not original cohort membership. Future or malformed tokens and scope mismatches are rejected.
     */
    baselineToken?: string,
  }): CancelablePromise<OperationWorkflowPerformanceSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-performance/summary',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'tenant_id': tenantId,
        'baseline_token': baselineToken,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Summarize completed workflows with explicit business outcomes.
   * Account outcome summary — Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment are required. Only latest retained terminal snapshots with reported outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals cover all matching instances independently of group pagination; cursors are separate from the attention queue. Reports and retention may change during browsing.
   * @returns OperationWorkflowOutcomeSummary All matching workflow count and a bounded group page ordered by value ascending.
   * @throws ApiError
   */
  public static summarizeAccountWorkflowOutcomes({
    slug,
    scope,
    workflow,
    code,
    tenantId,
    groupBy = 'outcome',
    limit = 20,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * For the account outcome summary, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account outcome summary, restrict terminal results to this explicit business outcome code.
     */
    code?: string,
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * For the account outcome summary, dimension used to aggregate matching workflow instances.
     */
    groupBy?: 'outcome' | 'workflow' | 'customer',
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowOutcomeSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-outcomes/summary',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'code': code,
        'tenant_id': tenantId,
        'group_by': groupBy,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Preview business workflow actions
   * Read-only candidates from the retained current state, evaluated with an empty evidence plan. Does not execute, authorize, reserve, or mutate an action. At most 100 actions; optional operation filter. Application must recheck locked rows and readiness before writing.
   * @returns OperationWorkflowActionPreviewResponse Observed transition readiness including reasons and advisories.
   * @throws ApiError
   */
  public static previewAccountWorkflowActions({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: OperationWorkflowActionPreviewRequest,
  }): CancelablePromise<OperationWorkflowActionPreviewResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-actions/preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Check reported requirements for a proposed workflow transition.
   * Requires account read scope and MFA. tenant_id is mandatory and app_id must be omitted; the path selects the owned application. Evaluates a proposed declared edge against retained state, target-specific blockers, direct prerequisites and planned milestone names. Ready is observational and grants no execution authority. Planned names are not committed evidence; transaction-time business checks and report validation still apply. A denied readiness result is a successful HTTP 200 response.
   * @returns OperationWorkflowReadinessResponse Account transition readiness — Observed transition readiness including reasons and advisories.
   * @throws ApiError
   */
  public static checkAccountWorkflowReadiness({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: OperationWorkflowReadinessRequest,
  }): CancelablePromise<OperationWorkflowReadinessResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Compare account transition readiness with a saved queue snapshot.
   * Requires account read scope and MFA; customer selectors stay within account ownership.
   * This read-only comparison accepts a caller-held snapshot emitted by the queue.
   * Snapshot scope binds the account, app, environment, customer selector and role.
   * Baseline data is advisory and is not authenticated historical evidence or execution authority.
   * At most 100 saved transitions are accepted in a body of at most 1048576 bytes.
   * Each distinct saved workflow is reread once with readiness-only projections,
   * independent of recent queue sampling and current owner or requirement filters.
   * The exact Operation/from/to edge and declared requirements must remain comparable.
   * Contract changes, terminal or unavailable state, unavailable edges and older retained
   * revisions are counted separately and never classified as newly ready or newly blocked.
   * Comparisons provide added and resolved requirement codes plus changed blocker,
   * prerequisite, pending-verification and reporting evidence categories.
   * Age counters alone are excluded from evidence fingerprints; freshness expiry can change readiness.
   * Change filters and the row limit apply after comparison; counts cover all saved transitions.
   * The returned snapshot includes available current edges before filtering and can be saved
   * as the next baseline. It omits unavailable edges. Reads are live and not atomic.
   * Empty snapshots are valid. Snapshot rows are limited to previously returned queue rows,
   * not the complete sampled backlog. Applications retain authorization and execution.
   *
   * @returns OperationWorkflowReadinessComparisonResponse Account readiness changes and comparison coverage over saved transitions.
   * @throws ApiError
   */
  public static compareAccountWorkflowReadiness({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: OperationWorkflowReadinessComparisonRequest,
  }): CancelablePromise<OperationWorkflowReadinessComparisonResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/compare',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
      },
    });
  }
  /**
   * List retained blocked, stale, overdue, or dependency-waiting business workflow instances.
   * Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated at the first page time carried by the cursor; current reports and retention can change during browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or applicable selected-contract edges on stale or overdue workflows. The queue grants no execution authority.
   * @returns OperationWorkflowAttentionResponse One bounded page ordered by latest report publication time descending.
   * @throws ApiError
   */
  public static listAccountWorkflowAttention({
    slug,
    scope,
    workflow,
    targetOperation,
    dependencyStatus,
    requiredOutcomeCode,
    priority,
    sort = 'updated_at',
    owner,
    unassigned,
    blockerCode,
    reason,
    tenantId,
    limit = 20,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * For the account attention queue, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account attention queue, restrict attention to blockers targeting this Operation name.
     */
    targetOperation?: string,
    /**
     * Filter unresolved direct prerequisites by status. Both dependency filters must match the same reference.
     */
    dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch',
    /**
     * For the account attention queue, restrict unresolved prerequisites to this required business outcome code.
     */
    requiredOutcomeCode?: string,
    /**
     * Exact effective blocker priority matched with owner and other blocker filters. Omitted blocker priority counts as normal.
     */
    priority?: 'low' | 'normal' | 'high' | 'urgent',
    /**
     * Queue ordering. Deadline sorts earliest business deadline first with undated workflows last. Summary groups remain in group-value order.
     */
    sort?: 'updated_at' | 'deadline',
    /**
     * Exact case-sensitive public application-assigned blocker owner, limited to 128 UTF-8 bytes without control characters. Mutually exclusive with unassigned=true; owner, blocker code, and target filters must match the same blocker.
     */
    owner?: string,
    /**
     * Select workflows with a matching blocker whose owner is empty or absent. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * For the account attention queue, restrict attention to this application-reported blocker code.
     */
    blockerCode?: string,
    /**
     * For the account attention queue, select the attention reason to include in the queue.
     */
    reason?: 'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk',
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowAttentionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-attention',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'target_operation': targetOperation,
        'dependency_status': dependencyStatus,
        'required_outcome_code': requiredOutcomeCode,
        'priority': priority,
        'sort': sort,
        'owner': owner,
        'unassigned': unassigned,
        'blocker_code': blockerCode,
        'reason': reason,
        'tenant_id': tenantId,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List account transition readiness across ongoing business workflows.
   * Requires account read scope and MFA. Optional customer selection stays within account ownership. Explicit app and environment required. Samples the 100 most recently updated retained ongoing workflows, ordered by update time then stable identity descending. Evaluates up to 100 sorted current-state transitions per workflow with no planned evidence. Target Operation filters before edge sampling; owner and requirement filters apply after evaluation but before row limit. Owner means an application-assigned blocker owner or pending verification owner for the target Operation; all retained pending obligations are considered before preview limits. Unassigned means one such finding has no owner. Reporting advisories never satisfy the requirement filter or change readiness. Unmet rows precede ready rows, preserving workflow sample order and Operation/from/to edge order. Sampling and row truncation are reported separately; counts cover evaluated transitions, not an account-wide backlog. Reads are live and not an atomic snapshot; changed workflow identities are excluded. No execution authority is granted. Unknown or terminal state is not enumerated. Duplicate, empty, and unsupported query parameters are rejected.
   * @returns OperationWorkflowReadinessQueueResponse Account transition readiness rows with explicit live sampling counts.
   * @throws ApiError
   */
  public static listAccountWorkflowReadinessQueue({
    slug,
    scope,
    workflow,
    targetOperation,
    owner,
    unassigned,
    tenantId,
    requirement,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * Account readiness queue. Optional declared workflow name within the app and environment.
     */
    workflow?: string,
    /**
     * Account readiness queue. Select declared transitions targeting this Operation name before edge sampling.
     */
    targetOperation?: string,
    /**
     * Account readiness queue. Exact case-sensitive blocker or pending verification owner for the transition Operation. Maximum 128 UTF-8 bytes without controls. Mutually exclusive with unassigned=true.
     */
    owner?: string,
    /**
     * Account readiness queue. Select transitions with an unassigned blocker or pending verification obligation. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * Account readiness queue. Optional customer UUID within the authenticated account.
     */
    tenantId?: string,
    /**
     * Account readiness queue. Select an unmet declared readiness requirement. Reporting advisories are excluded.
     */
    requirement?: 'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required' | 'retained_milestone_required' | 'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid',
    /**
     * Account readiness queue. Maximum matching transitions returned after filtering. No cursor is issued for this live sample.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowReadinessQueueResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/queue',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'target_operation': targetOperation,
        'owner': owner,
        'unassigned': unassigned,
        'tenant_id': tenantId,
        'requirement': requirement,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Summarize all matching retained workflow attention.
   * Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated at the first page time carried by the cursor; current reports and retention can change during browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or applicable selected-contract edges on stale or overdue workflows. Totals cover all matching retained snapshots regardless of group pagination. Groups may overlap and should not be added together. Blocker statistics within code and target groups cover that code or target only. Ages derive from application-reported first_observed_at; missing ages are counted separately. Group cursors are separate from queue cursors. The summary grants no execution authority.
   * @returns OperationWorkflowAttentionSummary Totals over all matching workflows and one bounded page of groups ordered by value ascending.
   * @throws ApiError
   */
  public static summarizeAccountWorkflowAttention({
    slug,
    scope,
    groupBy = 'workflow',
    workflow,
    targetOperation,
    dependencyStatus,
    requiredOutcomeCode,
    priority,
    sort = 'updated_at',
    owner,
    unassigned,
    blockerCode,
    reason,
    tenantId,
    limit = 20,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * For the account attention summary, dimension used to aggregate matching workflow instances.
     */
    groupBy?: 'owner' | 'workflow' | 'blocker_code' | 'target_operation' | 'customer' | 'dependency_status' | 'required_outcome_code',
    /**
     * For the account attention summary, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account attention summary, restrict attention to blockers targeting this Operation name.
     */
    targetOperation?: string,
    /**
     * Account attention summary — Filter unresolved direct prerequisites by status. Both dependency filters must match the same reference.
     */
    dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch',
    /**
     * For the account attention summary, restrict unresolved prerequisites to this required business outcome code.
     */
    requiredOutcomeCode?: string,
    /**
     * Account attention summary. Exact effective blocker priority matched with owner and other blocker filters. Omitted blocker priority counts as normal.
     */
    priority?: 'low' | 'normal' | 'high' | 'urgent',
    /**
     * Account attention summary. Queue ordering. Deadline sorts earliest business deadline first with undated workflows last. Summary groups remain in group-value order.
     */
    sort?: 'updated_at' | 'deadline',
    /**
     * Account attention summary. Exact case-sensitive public application-assigned blocker owner, limited to 128 UTF-8 bytes without control characters. Mutually exclusive with unassigned=true; owner, blocker code, and target filters must match the same blocker.
     */
    owner?: string,
    /**
     * Account attention summary. Select workflows with a matching blocker whose owner is empty or absent. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * For the account attention summary, restrict attention to this application-reported blocker code.
     */
    blockerCode?: string,
    /**
     * For the account attention summary, select the attention reason to include in the queue.
     */
    reason?: 'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk',
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowAttentionSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-attention/summary',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'group_by': groupBy,
        'workflow': workflow,
        'target_operation': targetOperation,
        'dependency_status': dependencyStatus,
        'required_outcome_code': requiredOutcomeCode,
        'priority': priority,
        'sort': sort,
        'owner': owner,
        'unassigned': unassigned,
        'blocker_code': blockerCode,
        'reason': reason,
        'tenant_id': tenantId,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Account create WorkflowReadinessSubscription
   * Account durable readiness subscription create operation. Observations are bounded and advisory; execution remains application-owned. Requires account deploy-write scope and MFA.
   * @returns OperationWorkflowReadinessSubscription Account Create readiness subscription result.
   * @throws ApiError
   */
  public static createAccountWorkflowReadinessSubscription({
    slug,
    requestBody,
  }: {
    /**
     * CreateAccountWorkflowReadinessSubscription. Application slug.
     */
    slug: string,
    /**
     * Account subscription selectors; bounded to 4096 bytes.
     */
    requestBody: OperationWorkflowReadinessSubscriptionRequest,
  }): CancelablePromise<OperationWorkflowReadinessSubscription> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Account list WorkflowReadinessSubscriptions
   * Account durable readiness subscription list operation. Observations are bounded and advisory; execution remains application-owned. Requires account read scope and MFA.
   * @returns OperationWorkflowReadinessSubscriptionsResponse Account List readiness subscription result.
   * @throws ApiError
   */
  public static listAccountWorkflowReadinessSubscriptions({
    slug,
    scope,
    tenantId,
  }: {
    /**
     * ListAccountWorkflowReadinessSubscriptions. Application slug.
     */
    slug: string,
    /**
     * ListAccountWorkflowReadinessSubscriptions. Application environment scope.
     */
    scope: string,
    /**
     * ListAccountWorkflowReadinessSubscriptions. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<OperationWorkflowReadinessSubscriptionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Account delete WorkflowReadinessSubscription
   * Account durable readiness subscription delete operation. Observations are bounded and advisory; execution remains application-owned. Requires account deploy-write scope and MFA.
   * @returns void
   * @throws ApiError
   */
  public static deleteAccountWorkflowReadinessSubscription({
    slug,
    id,
    scope,
    tenantId,
  }: {
    /**
     * DeleteAccountWorkflowReadinessSubscription. Application slug.
     */
    slug: string,
    /**
     * DeleteAccountWorkflowReadinessSubscription. Subscription UUID.
     */
    id: string,
    /**
     * DeleteAccountWorkflowReadinessSubscription. Application environment scope.
     */
    scope: string,
    /**
     * DeleteAccountWorkflowReadinessSubscription. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Account get WorkflowReadinessInbox
   * Account durable readiness subscription get operation. Observations are bounded and advisory; execution remains application-owned. Requires account read scope and MFA.
   * @returns OperationWorkflowReadinessInboxResponse Account Get readiness inbox page.
   * @throws ApiError
   */
  public static getAccountWorkflowReadinessInbox({
    slug,
    id,
    scope,
    tenantId,
    afterSequence,
    limit,
  }: {
    /**
     * GetAccountWorkflowReadinessInbox. Application slug.
     */
    slug: string,
    /**
     * GetAccountWorkflowReadinessInbox. Subscription UUID.
     */
    id: string,
    /**
     * GetAccountWorkflowReadinessInbox. Application environment scope.
     */
    scope: string,
    /**
     * GetAccountWorkflowReadinessInbox. Optional account customer selector.
     */
    tenantId?: string,
    /**
     * GetAccountWorkflowReadinessInbox. Last consumed sequence; defaults to zero.
     */
    afterSequence?: number,
    /**
     * GetAccountWorkflowReadinessInbox. Page size; defaults to 20.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowReadinessInboxResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}/notifications',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'after_sequence': afterSequence,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Check Account readiness notification
   * CheckAccountWorkflowReadinessNotification. Recheck retained application evidence; does not execute actions.
   * @returns OperationWorkflowReadinessNotificationCheck CheckAccountWorkflowReadinessNotification. Result.
   * @throws ApiError
   */
  public static checkAccountWorkflowReadinessNotification({
    slug,
    id,
    scope,
    sequence,
    tenantId,
  }: {
    /**
     * CheckAccountWorkflowReadinessNotification. Application slug.
     */
    slug: string,
    /**
     * CheckAccountWorkflowReadinessNotification. Subscription UUID.
     */
    id: string,
    /**
     * CheckAccountWorkflowReadinessNotification. Application environment scope.
     */
    scope: string,
    /**
     * CheckAccountWorkflowReadinessNotification. Retained subscription-local notification sequence.
     */
    sequence: number,
    /**
     * CheckAccountWorkflowReadinessNotification. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<OperationWorkflowReadinessNotificationCheck> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}/notifications/{sequence}',
      path: {
        'slug': slug,
        'id': id,
        'sequence': sequence,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Update Account readiness notification
   * UpdateAccountWorkflowReadinessNotification. Replace shared inbox coordination with a matching revision. Requires account deploy-write and MFA or customer operations manage. Actor derives from authentication. Revision zero addresses an untouched notification.
   * @returns OperationWorkflowReadinessNotification UpdateAccountWorkflowReadinessNotification. Result.
   * @throws ApiError
   */
  public static updateAccountWorkflowReadinessNotification({
    slug,
    id,
    scope,
    sequence,
    requestBody,
    tenantId,
  }: {
    /**
     * UpdateAccountWorkflowReadinessNotification. Application slug.
     */
    slug: string,
    /**
     * UpdateAccountWorkflowReadinessNotification. Subscription UUID.
     */
    id: string,
    /**
     * UpdateAccountWorkflowReadinessNotification. Application environment scope.
     */
    scope: string,
    /**
     * UpdateAccountWorkflowReadinessNotification. Retained subscription-local notification sequence.
     */
    sequence: number,
    requestBody: OperationWorkflowReadinessCoordinationRequest,
    /**
     * UpdateAccountWorkflowReadinessNotification. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<OperationWorkflowReadinessNotification> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}/notifications/{sequence}',
      path: {
        'slug': slug,
        'id': id,
        'sequence': sequence,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List Account readiness follow-up work queue
   * ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an explicit application and environment. Due-time ordering puts overdue first. Pages are live and may move; refresh after edits. Account reads require read scope and MFA; customer reads require Operations read.
   * @returns OperationWorkflowReadinessFollowUpsResponse ListAccountWorkflowReadinessFollowUps. Retained follow-up page.
   * @throws ApiError
   */
  public static listAccountWorkflowReadinessFollowUps({
    slug,
    scope,
    view,
    actionKind,
    actionTrigger,
    tenantId,
    owner,
    workflow,
    due,
    offset,
    limit,
    reminderOnly,
    impactPriority,
    impactKind,
    impactCode,
    sort,
  }: {
    /**
     * ListAccountWorkflowReadinessFollowUps. Application slug.
     */
    slug: string,
    /**
     * ListAccountWorkflowReadinessFollowUps. Application environment scope.
     */
    scope: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.view. Select follow-ups (default) or current cross-case next actions. Next-action view uses action ownership/deadlines; reminder and business-impact selectors are incompatible.
     */
    view?: 'follow_ups' | 'next_actions',
    /**
     * ListAccountWorkflowReadinessFollowUps.action_kind. Exact action kind in next-action view.
     */
    actionKind?: 'response_check_in' | 'decision_revalidation' | 'handoff_acceptance' | 'handoff_reassignment' | 'escalation_acknowledgment' | 'coordinator_completion' | 'scheduled_evidence_check' | 'coordinator_evidence_review' | 'coordinator_revision' | 'dependency_review' | 'follow_up' | 'fulfillment_review',
    /**
     * ListAccountWorkflowReadinessFollowUps.action_trigger. Exact trigger in next-action view.
     */
    actionTrigger?: 'human' | 'automatic' | 'suggested',
    /**
     * ListAccountWorkflowReadinessFollowUps.tenant_id. Optional customer selector matches notification evidence or a customer-specific subscription, including broader account watches.
     */
    tenantId?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.owner. Exact coordination owner, distinct from application blocker ownership.
     */
    owner?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.workflow. Exact workflow name; global evaluation failures have no workflow.
     */
    workflow?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.due. Defaults to all pending follow-ups.
     */
    due?: 'all' | 'overdue' | 'upcoming',
    /**
     * ListAccountWorkflowReadinessFollowUps.offset. Live zero-based offset, defaults to zero.
     */
    offset?: number,
    /**
     * ListAccountWorkflowReadinessFollowUps.limit. Page size, defaults to 20.
     */
    limit?: number,
    /**
     * ListAccountWorkflowReadinessFollowUps.reminder_only. Defaults to false; true selects active durable due reminders using the same owner, customer, workflow and environment filters.
     */
    reminderOnly?: boolean,
    /**
     * ListAccountWorkflowReadinessFollowUps.impact_priority. Exact pinned priority on an unresolved validity review; excludes ordinary follow-ups without review impact.
     */
    impactPriority?: 'normal' | 'high' | 'critical',
    /**
     * ListAccountWorkflowReadinessFollowUps.impact_kind. Protected business result category on an unresolved review.
     */
    impactKind?: 'milestone' | 'outcome',
    /**
     * ListAccountWorkflowReadinessFollowUps.impact_code. Exact protected result code; requires impact_kind to avoid milestone/outcome ambiguity.
     */
    impactCode?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.sort. Defaults to due. Business impact ordering puts overdue follow-ups first, then critical/high/normal unresolved review priority, then due time and stable notification identity. Other follow-ups have no declared review priority. Filtering and sorting precede live pagination within the retained candidate window.
     */
    sort?: 'due' | 'business_impact',
  }): CancelablePromise<OperationWorkflowReadinessFollowUpsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/follow-ups',
      path: {
        'slug': slug,
      },
      query: {
        'view': view,
        'action_kind': actionKind,
        'action_trigger': actionTrigger,
        'scope': scope,
        'tenant_id': tenantId,
        'owner': owner,
        'workflow': workflow,
        'due': due,
        'offset': offset,
        'limit': limit,
        'reminder_only': reminderOnly,
        'impact_priority': impactPriority,
        'impact_kind': impactKind,
        'impact_code': impactCode,
        'sort': sort,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read account workflow coordination timeline
   * GetAccountWorkflowCoordinationTimeline. Read a bounded chronological projection from current retained readiness inboxes for an exact subject and workflow instance. Observation clocks are server times; latest snapshots do not reconstruct every past edit. This is not a complete audit trail and does not authorize execution. Sources are read independently and live offset pages may move. Account access requires read scope and MFA; customer access requires Operations read.
   * @returns OperationWorkflowCoordinationTimeline GetAccountWorkflowCoordinationTimeline. Retained workflow coordination timeline with source coverage and limitations.
   * @throws ApiError
   */
  public static getAccountWorkflowCoordinationTimeline({
    slug,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    tenantId,
    offset,
    limit = 20,
  }: {
    /**
     * GetAccountWorkflowCoordinationTimeline.slug. Application slug.
     */
    slug: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.scope. Explicit application environment.
     */
    scope: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.tenant_id. Exact customer identity. Omission selects account-owned workflows only, not all customers.
     */
    tenantId?: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.offset. Live zero-based event offset. Refresh after edits or retention changes.
     */
    offset?: number,
    /**
     * GetAccountWorkflowCoordinationTimeline.limit. Maximum events per page.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowCoordinationTimeline> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/timeline',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
        'offset': offset,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read account workflow case overview
   * GetAccountWorkflowCase. Combine retained current application state, blockers, dependencies, verification and readiness with exact matching open follow-ups, advisory next steps and a retained coordination timeline. Evidence and coordination are independently read. Missing or stale reports cannot establish readiness or business success. The application retains outcome interpretation, authorization and execution. Account reads require an explicit customer selector, read scope and MFA; customer identity derives from authentication.
   * @returns OperationWorkflowCase GetAccountWorkflowCase. Current evidence and retained coordination for one exact business workflow case.
   * @throws ApiError
   */
  public static getAccountWorkflowCase({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    offset,
    limit = 20,
  }: {
    /**
     * GetAccountWorkflowCase.slug. Application slug.
     */
    slug: string,
    /**
     * GetAccountWorkflowCase.scope. Explicit application environment.
     */
    scope: string,
    /**
     * GetAccountWorkflowCase.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * GetAccountWorkflowCase.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * GetAccountWorkflowCase.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * GetAccountWorkflowCase.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * GetAccountWorkflowCase.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * GetAccountWorkflowCase.offset. Live event offset for the nested timeline only; follow-ups return their own bounded overview.
     */
    offset?: number,
    /**
     * GetAccountWorkflowCase.limit. Nested timeline page size; defaults to 20 with maximum 100.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowCase> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/case',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
        'offset': offset,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Start pinned workflow playbook progress
   * StartAccountWorkflowPlaybookProgress. Start from a currently matched fresh application playbook. Pin exact rule, workflow version and immutable reporting definition. Repeated starts return the existing checklist. Requires account write scope and MFA or customer operations-manage scope. Customer identity derives from authentication. Checklist completion does not establish readiness, authorize execution or report business success. At most 100 records per exact case; the latest 64 review changes per record are retained.
   * @returns OperationWorkflowPlaybookProgress StartAccountWorkflowPlaybookProgress. Durable pinned checklist after the accepted start or review.
   * @throws ApiError
   */
  public static startAccountWorkflowPlaybookProgress({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    requestBody,
  }: {
    /**
     * StartAccountWorkflowPlaybookProgress.slug. Application slug.
     */
    slug: string,
    /**
     * StartAccountWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * StartAccountWorkflowPlaybookProgress.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * StartAccountWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * StartAccountWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * StartAccountWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * StartAccountWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    requestBody: OperationWorkflowPlaybookProgressStartRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Update pinned workflow playbook progress
   * UpdateAccountWorkflowPlaybookProgress. Replace one frozen checklist item using the expected revision. Record authenticated reviewer, note and scoped operation or milestone evidence. Conflicts return 409. Existing guidance is never upgraded. Requires account write scope and MFA or customer operations-manage scope. Customer identity derives from authentication. Checklist completion does not establish readiness, authorize execution or report business success. At most 100 records per exact case; the latest 64 review changes per record are retained.
   * @returns OperationWorkflowPlaybookProgress UpdateAccountWorkflowPlaybookProgress. Durable pinned checklist after the accepted start or review.
   * @throws ApiError
   */
  public static updateAccountWorkflowPlaybookProgress({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * UpdateAccountWorkflowPlaybookProgress.slug. Application slug.
     */
    slug: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookProgressUpdateRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Configure account checklist evidence monitoring
   * ConfigureAccountWorkflowPlaybookEvidenceWatch. Explicitly enroll or stop a pinned checklist in an existing readiness inbox. Requires the exact checklist revision and watch configuration revision (zero for a new watch). Enabling captures a baseline without delivering a notice. Status or reason changes subsequently publish one grouped notification; clocks and report revisions alone do not notify. Account write scope and MFA or tenant operations-manage scope are required. Closure and revision are rechecked under the shared writer lock before publication. Polling does not change checklist progress or business state. At most 20 retained watches per subscription; stopped entries may be reclaimed for new watches.
   * @returns OperationWorkflowPlaybookEvidenceWatch ConfigureAccountWorkflowPlaybookEvidenceWatch. Watch configuration and saved evidence baseline.
   * @throws ApiError
   */
  public static configureAccountWorkflowPlaybookEvidenceWatch({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.slug. Application slug.
     */
    slug: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.scope. Explicit application environment.
     */
    scope: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookEvidenceWatchRequest,
  }): CancelablePromise<OperationWorkflowPlaybookEvidenceWatch> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress/{id}/evidence-watch',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Record account playbook review decision
   * ReviewAccountWorkflowPlaybookProgress. Record continue, retire or replace with a required human reason and expected source revision. Retire and replace permanently close source coordination. Replacement requires an already-started active applicable target in the same exact case, its expected revision, and an atomic target-state check. No item evidence, pinned guidance, business outcome or execution authority is changed. Account write scope and MFA or customer operations-manage scope are required; customer ownership derives from authentication. Applicability is observed before the write, not transactionally locked application evidence.
   * @returns OperationWorkflowPlaybookProgress ReviewAccountWorkflowPlaybookProgress. Source checklist with the committed durable decision and updated shared revision.
   * @throws ApiError
   */
  public static reviewAccountWorkflowPlaybookProgress({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * ReviewAccountWorkflowPlaybookProgress.slug. Application slug.
     */
    slug: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookReviewRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress/{id}/reviews',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
