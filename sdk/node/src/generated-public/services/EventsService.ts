/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ControlWorkflowRecoveryPolicyRequest } from '../models/ControlWorkflowRecoveryPolicyRequest.js';
import type { Problem } from '../models/Problem.js';
import type { SetWorkflowRecoveryPolicyRequest } from '../models/SetWorkflowRecoveryPolicyRequest.js';
import type { SuppressWorkflowRecoveryAutomaticActionRequest } from '../models/SuppressWorkflowRecoveryAutomaticActionRequest.js';
import type { WorkflowInitialEvidenceDiagnosis } from '../models/WorkflowInitialEvidenceDiagnosis.js';
import type { WorkflowInitialEvidenceRepairRequest } from '../models/WorkflowInitialEvidenceRepairRequest.js';
import type { WorkflowInitialFailures } from '../models/WorkflowInitialFailures.js';
import type { WorkflowInitialRecoveryPreview } from '../models/WorkflowInitialRecoveryPreview.js';
import type { WorkflowInitialRecoveryReview } from '../models/WorkflowInitialRecoveryReview.js';
import type { WorkflowInitialRecoveryReviewDecisionRequest } from '../models/WorkflowInitialRecoveryReviewDecisionRequest.js';
import type { WorkflowInitialRecoveryReviewRequest } from '../models/WorkflowInitialRecoveryReviewRequest.js';
import type { WorkflowInitialRecoveryReviews } from '../models/WorkflowInitialRecoveryReviews.js';
import type { WorkflowRecoveryAutomaticAction } from '../models/WorkflowRecoveryAutomaticAction.js';
import type { WorkflowRecoveryAutomaticQueue } from '../models/WorkflowRecoveryAutomaticQueue.js';
import type { WorkflowRecoveryHistory } from '../models/WorkflowRecoveryHistory.js';
import type { WorkflowRecoveryPolicyChangePreview } from '../models/WorkflowRecoveryPolicyChangePreview.js';
import type { WorkflowRecoveryPolicyEvaluation } from '../models/WorkflowRecoveryPolicyEvaluation.js';
import type { WorkflowRecoveryPolicyEvaluationRequest } from '../models/WorkflowRecoveryPolicyEvaluationRequest.js';
import type { WorkflowRecoveryPolicyHealth } from '../models/WorkflowRecoveryPolicyHealth.js';
import type { WorkflowRecoveryPolicyResponse } from '../models/WorkflowRecoveryPolicyResponse.js';
import type { WorkflowRecoveryPolicyReview } from '../models/WorkflowRecoveryPolicyReview.js';
import type { WorkflowRecoveryPolicyReviews } from '../models/WorkflowRecoveryPolicyReviews.js';
import type { WorkflowRecoveryReport } from '../models/WorkflowRecoveryReport.js';
import type { WorkflowRecoveryReviewDecisionRequest } from '../models/WorkflowRecoveryReviewDecisionRequest.js';
import type { WorkflowRecoveryReviewStatus } from '../models/WorkflowRecoveryReviewStatus.js';
import type { WorkflowRetryEvidenceDiagnosis } from '../models/WorkflowRetryEvidenceDiagnosis.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class EventsService {
  /**
   * Inspect the recovery policy
   * Returns the current app policy and scheduler evaluation status. An unconfigured app has a disabled version-zero policy.
   * @returns WorkflowRecoveryPolicyResponse Result of getWorkflowRecoveryPolicy.
   * @throws ApiError
   */
  public static getWorkflowRecoveryPolicy({
    slug,
  }: {
    /**
     * App slug for getWorkflowRecoveryPolicy.
     */
    slug: string,
  }): CancelablePromise<WorkflowRecoveryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Configure a recovery policy
   * Updates an app policy with optimistic version checks. Preview prepares plans from retained native evidence. Opt-in automatic_retry authorizes bounded retries of saved failures; automatic_initial authorizes bounded generation-zero recovery with explicit workflow and failure-status filters. Enabling automatic_initial requires the workflow runtime. Requires deploy write permission and MFA; cancellation remains manual.
   * @returns WorkflowRecoveryPolicyResponse Result of setWorkflowRecoveryPolicy.
   * @throws ApiError
   */
  public static setWorkflowRecoveryPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug for setWorkflowRecoveryPolicy.
     */
    slug: string,
    /**
     * Recovery policy settings and explicit automatic retry limits with the last observed version.
     */
    requestBody: SetWorkflowRecoveryPolicyRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/recovery-policy',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Evaluate proposed recovery settings against retained historical candidates
   * Read-only consistent snapshot comparing current and proposed settings against up to 100 oldest retained reviews and 500 decisions in a maximum 30-day window. Requires the observed current policy version. Individual historical failure status is not captured; historical policy filters establish a possible status set and ambiguous narrower filters remain unknown. Preparation budgets, cooldown and deduplication are replayed conditionally from empty window-start state; unknown candidates are not charged. Actual recorded applications establish lower-bound execution pressure, never admission availability or counterfactual retry results. Application warmup uses the longer policy cooldown or one hour, with up to 10000 records and explicit truncation. Never reads current run state as historical proof or writes policy, reviews, jobs, audit events or notifications.
   * @returns WorkflowRecoveryPolicyEvaluation Evidence-limited conditional comparison with explicit coverage notes.
   * @throws ApiError
   */
  public static evaluateWorkflowRecoveryPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App whose retained review evidence is used for policy comparison.
     */
    slug: string,
    requestBody: WorkflowRecoveryPolicyEvaluationRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyEvaluation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/evaluate',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Current recovery policy version differs from expected_version.`,
      },
    });
  }
  /**
   * Preview a recovery policy replacement
   * Read-only impact of replacing the observed policy version. Every replacement suppresses old pending automatic intents and preserves unresolved applied children and spent charges. Inspects up to five newest matching recovery jobs and returns up to 25 candidate decisions. Retained preparation targets continue to prevent duplicate plans. Requires read access and MFA. No plans, controls, retries or notifications are written. Advisory evidence can change before a separately authorized version-fenced update.
   * @returns WorkflowRecoveryPolicyChangePreview Read-only proposed-policy snapshot with parent-job and bounded initial-failure candidates. No reviews, jobs or notifications are created.
   * @throws ApiError
   */
  public static previewWorkflowRecoveryPolicyChange({
    slug,
    requestBody,
  }: {
    /**
     * App slug for previewWorkflowRecoveryPolicyChange.
     */
    slug: string,
    /**
     * Proposed policy using the last observed configuration version; version zero means no existing policy.
     */
    requestBody: SetWorkflowRecoveryPolicyRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyChangePreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/change-preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        504: `Policy change preview inspection timed out.`,
      },
    });
  }
  /**
   * Pause or resume automatic recovery
   * Controls enabled automatic recovery without replacing its policy or discarding prepared retries. Requires deploy write and MFA. The observed version, update timestamp and pause timestamp fence concurrent controls and failure pauses. Resume clears the consecutive failure streak; committed children continue while paused.
   * @returns WorkflowRecoveryPolicyResponse Result of controlWorkflowRecoveryPolicy.
   * @throws ApiError
   */
  public static controlWorkflowRecoveryPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug for controlWorkflowRecoveryPolicy.
     */
    slug: string,
    /**
     * Observed recovery control state, operation and operator reason.
     */
    requestBody: ControlWorkflowRecoveryPolicyRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/control',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Prepare a durable first-recovery review
   * Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReview Initial recovery review
   * @throws ApiError
   */
  public static prepareWorkflowInitialRecoveryReview({
    slug,
    failureId,
    requestBody,
  }: {
    /**
     * Application slug.
     */
    slug: string,
    /**
     * Captured initial workflow failure identifier.
     */
    failureId: string,
    requestBody: WorkflowInitialRecoveryReviewRequest,
  }): CancelablePromise<WorkflowInitialRecoveryReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-failures/{failureID}/recovery-reviews',
      path: {
        'slug': slug,
        'failureID': failureId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Diagnose pending retry parent evidence
   * Read-only diagnosis of the exact parent generation and item required by the frozen pending retry plan. Missing admission history and superseded runs cannot be repaired. Requires read scope and MFA.
   * @returns WorkflowRetryEvidenceDiagnosis Consistent retained evidence diagnosis.
   * @returns Problem Error.
   * @throws ApiError
   */
  public static diagnoseWorkflowRetryParentEvidence({
    slug,
    reviewId,
    actionId,
  }: {
    /**
     * Application slug used to diagnose pending retry parent evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier.
     */
    actionId: string,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/parent',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
    });
  }
  /**
   * Save verified pending retry parent terminal evidence
   * Requires deploy write and MFA. Re-inspects the exact parent generation, item and admission lineage under lock. Repair applies only to pending actions and does not authorize child submission. Parent and child repair receipts are separate. Saves a missing result once and records the operator reason. Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and reason returns its receipt; changes conflict. The worker observes repaired proof on its next eligible poll.
   * @returns WorkflowRetryEvidenceDiagnosis Retained repair receipt and evidence diagnosis.
   * @returns Problem Save verified pending retry parent terminal evidence failed.
   * @throws ApiError
   */
  public static repairWorkflowRetryParentEvidence({
    slug,
    reviewId,
    actionId,
    requestBody,
  }: {
    /**
     * Application slug used to save verified pending retry parent terminal evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to save verified pending retry parent terminal evidence.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier used to save verified pending retry parent terminal evidence.
     */
    actionId: string,
    requestBody: WorkflowInitialEvidenceRepairRequest,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/parent/repair',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Diagnose retained automatic retry evidence
   * Read-only exact retry-generation diagnosis with frozen parent and child lineage checks. Missing admission history and superseded runs cannot be repaired. Requires read scope and MFA.
   * @returns WorkflowRetryEvidenceDiagnosis Diagnose retained automatic retry evidence. Consistent retained evidence diagnosis.
   * @returns Problem Diagnose retained automatic retry evidence failed.
   * @throws ApiError
   */
  public static diagnoseWorkflowRetryEvidence({
    slug,
    reviewId,
    actionId,
  }: {
    /**
     * Application slug used to diagnose retained automatic retry evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to diagnose retained automatic retry evidence.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier used to diagnose retained automatic retry evidence.
     */
    actionId: string,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
    });
  }
  /**
   * Save verified automatic retry terminal evidence
   * Requires deploy write and MFA. Re-inspects the exact generation and admission lineage under lock. Saves a missing result once and records the operator reason. Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and reason returns its receipt; changes conflict. The worker observes repaired proof on its next eligible poll.
   * @returns WorkflowRetryEvidenceDiagnosis Save verified automatic retry terminal evidence. Retained repair receipt and evidence diagnosis.
   * @returns Problem Save verified automatic retry terminal evidence failed.
   * @throws ApiError
   */
  public static repairWorkflowRetryEvidence({
    slug,
    reviewId,
    actionId,
    requestBody,
  }: {
    /**
     * Application slug used to save verified automatic retry terminal evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to save verified automatic retry terminal evidence.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier used to save verified automatic retry terminal evidence.
     */
    actionId: string,
    requestBody: WorkflowInitialEvidenceRepairRequest,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/repair',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Diagnose retained initial recovery evidence
   * Read-only exact-generation diagnosis. Missing admission history and superseded runs cannot be repaired. Requires read scope and MFA.
   * @returns WorkflowInitialEvidenceDiagnosis Diagnose retained initial recovery evidence. Consistent retained evidence diagnosis.
   * @returns Problem Diagnose retained initial recovery evidence failed.
   * @throws ApiError
   */
  public static diagnoseWorkflowInitialEvidence({
    slug,
    reviewId,
  }: {
    /**
     * Application slug used to diagnose retained initial recovery evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to diagnose retained initial recovery evidence.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowInitialEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
    });
  }
  /**
   * Save verified initial recovery terminal evidence
   * Save verified initial recovery terminal evidence. Requires deploy write and MFA. Re-inspects the exact generation and admission lineage under lock. Saves a missing result once and records the operator reason. Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and reason returns its receipt; changes conflict. The worker observes repaired proof on its next eligible poll.
   * @returns WorkflowInitialEvidenceDiagnosis Save verified initial recovery terminal evidence. Retained repair receipt and evidence diagnosis.
   * @returns Problem Save verified initial recovery terminal evidence failed.
   * @throws ApiError
   */
  public static repairWorkflowInitialEvidence({
    slug,
    reviewId,
    requestBody,
  }: {
    /**
     * Application slug used to save verified initial recovery terminal evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to save verified initial recovery terminal evidence.
     */
    reviewId: string,
    requestBody: WorkflowInitialEvidenceRepairRequest,
  }): CancelablePromise<WorkflowInitialEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence/repair',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Inspect a durable first-recovery review
   * Inspect a durable first-recovery review. Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReview Inspect a durable first-recovery review result.
   * @throws ApiError
   */
  public static getWorkflowInitialRecoveryReview({
    slug,
    reviewId,
  }: {
    /**
     * Application slug for inspect a durable first-recovery review.
     */
    slug: string,
    /**
     * Durable recovery review identifier for inspect a durable first-recovery review.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowInitialRecoveryReview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Submit or cancel a durable first-recovery review
   * Submit or cancel a durable first-recovery review. Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReview Submit or cancel a durable first-recovery review result.
   * @throws ApiError
   */
  public static decideWorkflowInitialRecoveryReview({
    slug,
    reviewId,
    requestBody,
  }: {
    /**
     * Application slug used to submit or cancel a durable first-recovery review.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to submit or cancel a durable first-recovery review.
     */
    reviewId: string,
    requestBody: WorkflowInitialRecoveryReviewDecisionRequest,
  }): CancelablePromise<WorkflowInitialRecoveryReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}/decision',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * List retained first-recovery reviews
   * List retained first-recovery reviews. Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReviews List retained first-recovery reviews result.
   * @throws ApiError
   */
  public static listWorkflowInitialRecoveryReviews({
    slug,
    after,
    limit = 100,
  }: {
    /**
     * Application slug used to list retained first-recovery reviews.
     */
    slug: string,
    /**
     * Opaque cursor from the previous page.
     */
    after?: string,
    /**
     * Maximum number of results to return.
     */
    limit?: number,
  }): CancelablePromise<WorkflowInitialRecoveryReviews> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/initial-recovery-reviews',
      path: {
        'slug': slug,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List captured initial workflow failures
   * Read-only, policy-independent generation-zero failure inbox. Capture is prospective and partial, retained for 30 days with at most 10000 entries per app. Newest-first cursors bind the account, app, workflow filter and observation ceiling. Retention may remove entries between pages. Evidence alone does not prove that a run can safely resume. No run inputs, outputs or error text are returned.
   * @returns WorkflowInitialFailures Retained initial failures.
   * @throws ApiError
   */
  public static listWorkflowInitialFailures({
    slug,
    workflow,
    after,
    limit = 25,
  }: {
    /**
     * Application slug used to list captured initial workflow failures.
     */
    slug: string,
    /**
     * Workflow name to filter captured failures.
     */
    workflow?: string,
    /**
     * Opaque cursor from the previous page used to list captured initial workflow failures.
     */
    after?: string,
    /**
     * Maximum number of results to return used to list captured initial workflow failures.
     */
    limit?: number,
  }): CancelablePromise<WorkflowInitialFailures> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-failures',
      path: {
        'slug': slug,
      },
      query: {
        'workflow': workflow,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Review first recovery eligibility for a captured failure
   * Read-only consistent live inspection of retained generation-zero evidence, current generation, safe resume diagnostics and operation ownership. Returns a generation-fenced target only when eligible. Cached later eligibility and policy existence do not authorize recovery. This does not prepare a policy review or admit a run. Operators may submit the exact reviewed target through event recovery creation with a stable request_id; creation and worker admission recheck evidence retention and generation, and resume rechecks safety and capacity. No automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryPreview Live first recovery diagnostic.
   * @throws ApiError
   */
  public static previewWorkflowInitialRecovery({
    slug,
    failureId,
  }: {
    /**
     * Application slug for review first recovery eligibility for a captured failure.
     */
    slug: string,
    /**
     * Captured initial workflow failure identifier for review first recovery eligibility for a captured failure.
     */
    failureId: string,
  }): CancelablePromise<WorkflowInitialRecoveryPreview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-failures/{failureID}/recovery-preview',
      path: {
        'slug': slug,
        'failureID': failureId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect retained recovery automation history
   * Read-only timeline of retained review decisions, controls, retry events, durable policy changes, worker reason transitions and health incident events, alongside latest observations. Durable events are captured prospectively with their state mutations, retained for 30 days and capped at 10000 per app. Results are ordered newest first with stable identifier tie breaks. Cursors fix the time window and durable event sequence ceiling; later appends are excluded. Retention can remove events, and latest observations can change across pages. Workflow, run and policy filters omit records without those associations. No execution inputs or outputs are returned.
   * @returns WorkflowRecoveryHistory Retained history page.
   * @throws ApiError
   */
  public static getWorkflowRecoveryHistory({
    slug,
    workflow,
    runId,
    policyVersion,
    createdAfter,
    createdBefore,
    after,
    limit = 25,
  }: {
    /**
     * App slug.
     */
    slug: string,
    /**
     * Workflow name filter.
     */
    workflow?: string,
    /**
     * Workflow run filter.
     */
    runId?: string,
    /**
     * Policy version filter; zero includes all versions.
     */
    policyVersion?: number,
    /**
     * Inclusive start; defaults to seven days before the end.
     */
    createdAfter?: string,
    /**
     * Inclusive end; defaults to the first page observation time. Maximum window is 30 days.
     */
    createdBefore?: string,
    /**
     * Opaque next_after cursor; preserve the original filters.
     */
    after?: string,
    /**
     * Page size; zero uses the default.
     */
    limit?: number,
  }): CancelablePromise<WorkflowRecoveryHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/history',
      path: {
        'slug': slug,
      },
      query: {
        'workflow': workflow,
        'run_id': runId,
        'policy_version': policyVersion,
        'created_after': createdAfter,
        'created_before': createdBefore,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Report observed recovery automation effectiveness
   * Read-only consistent snapshot summarizing at most 10000 newest retained history entries. Deduplicates durable and retained retry records by action. Attempts and results are selected independently by event time; missing results do not prove children are running. Durations measure application to result recording including worker polling delay. Groups use workflow and policy version. Blockers count distinct actions per observed reason. Recording is prospective, without backfill, and durable history retains 30 days / 10000 events per app. Coverage notes and truncation always accompany counts; no success percentage or causal effectiveness claim is inferred.
   * @returns WorkflowRecoveryReport Observed effectiveness report.
   * @throws ApiError
   */
  public static getWorkflowRecoveryReport({
    slug,
    workflow,
    policyVersion,
    createdAfter,
    createdBefore,
  }: {
    /**
     * App whose observed recovery behavior is summarized.
     */
    slug: string,
    /**
     * Restrict report evidence to this workflow name.
     */
    workflow?: string,
    /**
     * Restrict report evidence to this policy version; zero includes every version.
     */
    policyVersion?: number,
    /**
     * Inclusive report start; omitted values use seven days before the report end.
     */
    createdAfter?: string,
    /**
     * Inclusive end; defaults to the snapshot observation time. Maximum window is 30 days.
     */
    createdBefore?: string,
  }): CancelablePromise<WorkflowRecoveryReport> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/report',
      path: {
        'slug': slug,
      },
      query: {
        'workflow': workflow,
        'policy_version': policyVersion,
        'created_after': createdAfter,
        'created_before': createdBefore,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect recovery automation health
   * Read-only app-scoped recovery policy health from a consistent bounded queue snapshot. Expected cooldown and exhausted budgets are blocked rather than stalled. Stalled means worker evaluation is overdue, not that a running child exceeded its execution deadline. This is advisory and never releases slots or authorizes execution.
   * @returns WorkflowRecoveryPolicyHealth Observed recovery automation health.
   * @throws ApiError
   */
  public static getWorkflowRecoveryPolicyHealth({
    slug,
    stallAfterSeconds = 300,
  }: {
    /**
     * App slug for recovery policy health.
     */
    slug: string,
    /**
     * Worker evaluation age threshold; request-local and not persisted.
     */
    stallAfterSeconds?: number,
  }): CancelablePromise<WorkflowRecoveryPolicyHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/health',
      path: {
        'slug': slug,
      },
      query: {
        'stall_after_seconds': stallAfterSeconds,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect queued automatic retries
   * Returns a consistent app-scoped snapshot of current pending intents, unresolved applied children across policy versions, and execution charges across all versions. Includes at most 100 entries in review order. Reasons reflect the last worker evaluation and are informational; unknown evidence never grants execution authority.
   * @returns WorkflowRecoveryAutomaticQueue Result of getWorkflowRecoveryAutomaticQueue.
   * @throws ApiError
   */
  public static getWorkflowRecoveryAutomaticQueue({
    slug,
  }: {
    /**
     * App slug for getWorkflowRecoveryAutomaticQueue.
     */
    slug: string,
  }): CancelablePromise<WorkflowRecoveryAutomaticQueue> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/queue',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List retained recovery policy decisions
   * Lists immutable evaluations and prepared response plans in identifier order. Evidence is retained with the parent recovery job.
   * @returns WorkflowRecoveryPolicyReviews Result of listWorkflowRecoveryPolicyReviews.
   * @throws ApiError
   */
  public static listWorkflowRecoveryPolicyReviews({
    slug,
    after,
    limit = 100,
  }: {
    /**
     * App slug for listWorkflowRecoveryPolicyReviews.
     */
    slug: string,
    /**
     * Exclusive review identifier cursor from the previous policy review page.
     */
    after?: string,
    /**
     * Maximum retained policy reviews returned in this page.
     */
    limit?: number,
  }): CancelablePromise<WorkflowRecoveryPolicyReviews> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews',
      path: {
        'slug': slug,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect one retained policy review
   * Reads a frozen policy version and its target decisions. A prepared plan can be exported and explicitly applied using generation-fenced controls.
   * @returns WorkflowRecoveryPolicyReview Result of getWorkflowRecoveryPolicyReview.
   * @throws ApiError
   */
  public static getWorkflowRecoveryPolicyReview({
    slug,
    reviewId,
  }: {
    /**
     * App slug for getWorkflowRecoveryPolicyReview.
     */
    slug: string,
    /**
     * Retained recovery policy review to inspect.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowRecoveryPolicyReview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Approve or dismiss a policy plan
   * Records one immutable actor-attributed decision. An identical retry returns the original decision; a different decision or reason conflicts. Approval never applies actions and can be recorded for stale plans. Only reviews with prepared plans can be decided.
   * @returns WorkflowRecoveryPolicyReview Review operation result.
   * @throws ApiError
   */
  public static decideWorkflowRecoveryPolicyReview({
    slug,
    reviewId,
    requestBody,
  }: {
    /**
     * App owning the policy review.
     */
    slug: string,
    /**
     * Retained policy review identifier.
     */
    reviewId: string,
    requestBody: WorkflowRecoveryReviewDecisionRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/decision',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Suppress one pending automatic retry
   * Suppresses a retained pending intent under its observed policy version. Records a trusted operator and reason. Requires deploy write and MFA. Applied children cannot be suppressed; no child is cancelled and no charge is refunded. Identical reason retries return the retained audit receipt while the policy version remains current. Manual recovery remains available.
   * @returns WorkflowRecoveryAutomaticAction Retained result of suppressWorkflowRecoveryAutomaticAction.
   * @throws ApiError
   */
  public static suppressWorkflowRecoveryAutomaticAction({
    slug,
    reviewId,
    actionId,
    requestBody,
  }: {
    /**
     * App slug for suppressWorkflowRecoveryAutomaticAction.
     */
    slug: string,
    /**
     * Retained review containing the automatic retry to suppress.
     */
    reviewId: string,
    /**
     * Stable pending automatic action identifier for suppression.
     */
    actionId: string,
    /**
     * Observed automatic policy version and operator suppression reason.
     */
    requestBody: SuppressWorkflowRecoveryAutomaticActionRequest,
  }): CancelablePromise<WorkflowRecoveryAutomaticAction> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/suppress',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Inspect live policy plan freshness
   * Checks retained action receipts before live generation eligibility. Reports ready, stale, unknown, applied, partially_applied or no_plan. Applied means a cancellation succeeded or a retry child was created; execution outcomes require the response-status command. Advisory checks never mutate or refresh plans.
   * @returns WorkflowRecoveryReviewStatus Current advisory freshness and durable application evidence.
   * @throws ApiError
   */
  public static getWorkflowRecoveryReviewStatus({
    slug,
    reviewId,
  }: {
    /**
     * App whose prepared response plan freshness is inspected.
     */
    slug: string,
    /**
     * Policy review containing the saved targets to inspect.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowRecoveryReviewStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/status',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
}
