/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AdoptEnvironmentGitOpsRequest } from '../models/AdoptEnvironmentGitOpsRequest.js';
import type { AdoptEnvironmentGitOpsResponse } from '../models/AdoptEnvironmentGitOpsResponse.js';
import type { ApplyCachedProjectPlanRequest } from '../models/ApplyCachedProjectPlanRequest.js';
import type { ApplyResponse } from '../models/ApplyResponse.js';
import type { ApproveEnvironmentGitRevisionRequest } from '../models/ApproveEnvironmentGitRevisionRequest.js';
import type { ApproveEnvironmentGitRevisionResponse } from '../models/ApproveEnvironmentGitRevisionResponse.js';
import type { BindingVerificationRunList } from '../models/BindingVerificationRunList.js';
import type { BrowserPromotionApprovalExchange } from '../models/BrowserPromotionApprovalExchange.js';
import type { BrowserPromotionApprovalRequest } from '../models/BrowserPromotionApprovalRequest.js';
import type { BrowserPromotionApprovalResponse } from '../models/BrowserPromotionApprovalResponse.js';
import type { CheckedProjectReleaseManifest } from '../models/CheckedProjectReleaseManifest.js';
import type { CheckedProjectReleaseResponse } from '../models/CheckedProjectReleaseResponse.js';
import type { CreateEnvironmentGitSourceRequest } from '../models/CreateEnvironmentGitSourceRequest.js';
import type { CreateProjectBindingVerificationRunRequest } from '../models/CreateProjectBindingVerificationRunRequest.js';
import type { CreateProjectEnvironmentApprovalRequest } from '../models/CreateProjectEnvironmentApprovalRequest.js';
import type { CreateProjectEnvironmentQualificationRequest } from '../models/CreateProjectEnvironmentQualificationRequest.js';
import type { CreateProjectEnvironmentRequest } from '../models/CreateProjectEnvironmentRequest.js';
import type { CreateProjectMigrationRequest } from '../models/CreateProjectMigrationRequest.js';
import type { DetachEnvironmentGitSourceRequest } from '../models/DetachEnvironmentGitSourceRequest.js';
import type { EnvironmentFieldOwnershipRequest } from '../models/EnvironmentFieldOwnershipRequest.js';
import type { EnvironmentFieldOwnershipResponse } from '../models/EnvironmentFieldOwnershipResponse.js';
import type { EnvironmentGitOpsOverrideRequest } from '../models/EnvironmentGitOpsOverrideRequest.js';
import type { EnvironmentGitOpsPlan } from '../models/EnvironmentGitOpsPlan.js';
import type { EnvironmentGitOpsStatusResponse } from '../models/EnvironmentGitOpsStatusResponse.js';
import type { EnvironmentGitSource } from '../models/EnvironmentGitSource.js';
import type { EnvironmentGitSourceUpdate } from '../models/EnvironmentGitSourceUpdate.js';
import type { ListEnvironmentGitReleasesResponse } from '../models/ListEnvironmentGitReleasesResponse.js';
import type { PlanResponse } from '../models/PlanResponse.js';
import type { PrepareProjectRollbackRequest } from '../models/PrepareProjectRollbackRequest.js';
import type { PreviewEnvironmentGitPlanResponse } from '../models/PreviewEnvironmentGitPlanResponse.js';
import type { PreviewEnvironmentGitPromotionRequest } from '../models/PreviewEnvironmentGitPromotionRequest.js';
import type { PreviewEnvironmentGitPromotionResponse } from '../models/PreviewEnvironmentGitPromotionResponse.js';
import type { PreviewEnvironmentGitRevisionRequest } from '../models/PreviewEnvironmentGitRevisionRequest.js';
import type { PreviewEnvironmentGitRevisionResponse } from '../models/PreviewEnvironmentGitRevisionResponse.js';
import type { PreviewEnvironmentGitRollbackRequest } from '../models/PreviewEnvironmentGitRollbackRequest.js';
import type { PreviewEnvironmentGitRollbackResponse } from '../models/PreviewEnvironmentGitRollbackResponse.js';
import type { Problem } from '../models/Problem.js';
import type { ProjectApplyRequest } from '../models/ProjectApplyRequest.js';
import type { ProjectBindingVerificationReview } from '../models/ProjectBindingVerificationReview.js';
import type { ProjectBindingVerificationRun } from '../models/ProjectBindingVerificationRun.js';
import type { ProjectBindingVerificationRunDiagnostics } from '../models/ProjectBindingVerificationRunDiagnostics.js';
import type { ProjectDeletePreviewResponse } from '../models/ProjectDeletePreviewResponse.js';
import type { ProjectDeploymentCheckRequest } from '../models/ProjectDeploymentCheckRequest.js';
import type { ProjectDeploymentReadiness } from '../models/ProjectDeploymentReadiness.js';
import type { ProjectEnvironmentApprovalResponse } from '../models/ProjectEnvironmentApprovalResponse.js';
import type { ProjectEnvironmentApprovalStatusResponse } from '../models/ProjectEnvironmentApprovalStatusResponse.js';
import type { ProjectEnvironmentCloneOperationResponse } from '../models/ProjectEnvironmentCloneOperationResponse.js';
import type { ProjectEnvironmentConfigDiffResponse } from '../models/ProjectEnvironmentConfigDiffResponse.js';
import type { ProjectEnvironmentConfigResponse } from '../models/ProjectEnvironmentConfigResponse.js';
import type { ProjectEnvironmentDiffResponse } from '../models/ProjectEnvironmentDiffResponse.js';
import type { ProjectEnvironmentEdgePolicyResponse } from '../models/ProjectEnvironmentEdgePolicyResponse.js';
import type { ProjectEnvironmentPromotionListResponse } from '../models/ProjectEnvironmentPromotionListResponse.js';
import type { ProjectEnvironmentPromotionPreviewResponse } from '../models/ProjectEnvironmentPromotionPreviewResponse.js';
import type { ProjectEnvironmentPromotionResponse } from '../models/ProjectEnvironmentPromotionResponse.js';
import type { ProjectEnvironmentPromotionSecretPreviewRequest } from '../models/ProjectEnvironmentPromotionSecretPreviewRequest.js';
import type { ProjectEnvironmentPromotionStatusResponse } from '../models/ProjectEnvironmentPromotionStatusResponse.js';
import type { ProjectEnvironmentQualificationResponse } from '../models/ProjectEnvironmentQualificationResponse.js';
import type { ProjectEnvironmentQueueBindingsResponse } from '../models/ProjectEnvironmentQueueBindingsResponse.js';
import type { ProjectEnvironmentReleaseListResponse } from '../models/ProjectEnvironmentReleaseListResponse.js';
import type { ProjectEnvironmentResponse } from '../models/ProjectEnvironmentResponse.js';
import type { ProjectEnvironmentRoutePolicyResponse } from '../models/ProjectEnvironmentRoutePolicyResponse.js';
import type { ProjectEnvironmentSharedSecretResponse } from '../models/ProjectEnvironmentSharedSecretResponse.js';
import type { ProjectEnvironmentSharedSecretsResponse } from '../models/ProjectEnvironmentSharedSecretsResponse.js';
import type { ProjectEnvironmentStateResponse } from '../models/ProjectEnvironmentStateResponse.js';
import type { ProjectMigration } from '../models/ProjectMigration.js';
import type { ProjectMigrationList } from '../models/ProjectMigrationList.js';
import type { ProjectMigrationRecoveryRequest } from '../models/ProjectMigrationRecoveryRequest.js';
import type { ProjectPromotionControl } from '../models/ProjectPromotionControl.js';
import type { ProjectPromotionControlRequest } from '../models/ProjectPromotionControlRequest.js';
import type { ProjectPromotionMigrationRecovery } from '../models/ProjectPromotionMigrationRecovery.js';
import type { ProjectPromotionMigrationRecoveryRequest } from '../models/ProjectPromotionMigrationRecoveryRequest.js';
import type { ProjectReleaseControlRequest } from '../models/ProjectReleaseControlRequest.js';
import type { ProjectReleaseSecretStageRequest } from '../models/ProjectReleaseSecretStageRequest.js';
import type { ProjectReleaseSecretStageResponse } from '../models/ProjectReleaseSecretStageResponse.js';
import type { ProjectReleaseSetListResponse } from '../models/ProjectReleaseSetListResponse.js';
import type { ProjectReleaseSetResponse } from '../models/ProjectReleaseSetResponse.js';
import type { ProjectReleaseStage } from '../models/ProjectReleaseStage.js';
import type { ProjectResponse } from '../models/ProjectResponse.js';
import type { ProjectScanRequest } from '../models/ProjectScanRequest.js';
import type { ProjectSourceRefScanRequest } from '../models/ProjectSourceRefScanRequest.js';
import type { ProjectSubmissionReceipt } from '../models/ProjectSubmissionReceipt.js';
import type { ProjectSummaryResponse } from '../models/ProjectSummaryResponse.js';
import type { PromoteProjectEnvironmentRequest } from '../models/PromoteProjectEnvironmentRequest.js';
import type { PublishProjectReleaseSetRequest } from '../models/PublishProjectReleaseSetRequest.js';
import type { RebindEnvironmentGitSourceRequest } from '../models/RebindEnvironmentGitSourceRequest.js';
import type { RemoveEnvironmentGitOpsOverrideRequest } from '../models/RemoveEnvironmentGitOpsOverrideRequest.js';
import type { ReplaceProjectEnvironmentQueueBindingsRequest } from '../models/ReplaceProjectEnvironmentQueueBindingsRequest.js';
import type { ResumeProjectMigrationRequest } from '../models/ResumeProjectMigrationRequest.js';
import type { ReviewProjectBindingVerificationRequest } from '../models/ReviewProjectBindingVerificationRequest.js';
import type { SetProjectEnvironmentSharedSecretRequest } from '../models/SetProjectEnvironmentSharedSecretRequest.js';
import type { StageProjectReleaseRequest } from '../models/StageProjectReleaseRequest.js';
import type { UpdateProjectEnvironmentConfigRequest } from '../models/UpdateProjectEnvironmentConfigRequest.js';
import type { UpdateProjectEnvironmentEdgePolicyRequest } from '../models/UpdateProjectEnvironmentEdgePolicyRequest.js';
import type { UpdateProjectEnvironmentRequest } from '../models/UpdateProjectEnvironmentRequest.js';
import type { UpdateProjectEnvironmentRoutePolicyRequest } from '../models/UpdateProjectEnvironmentRoutePolicyRequest.js';
import type { UpdateProjectRequest } from '../models/UpdateProjectRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ProjectsService {
  /**
   * Save and apply a reviewed multi-service migration
   * Save and apply a reviewed multi-service migration. Source-backed HTTP and GraphQL services only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No domain or production traffic changes are performed by this endpoint.
   * @returns ProjectMigration Save and apply a reviewed multi-service migration result.
   * @returns Problem Save and apply a reviewed multi-service migration error.
   * @throws ApiError
   */
  public static createProjectMigration({
    requestBody,
  }: {
    requestBody: CreateProjectMigrationRequest,
  }): CancelablePromise<ProjectMigration | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/project-migrations',
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * List saved project migrations for the authenticated account
   * List saved project migrations for the authenticated account. Source-backed HTTP and GraphQL services only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No domain or production traffic changes are performed by this endpoint.
   * @returns ProjectMigrationList List saved project migrations for the authenticated account result.
   * @returns Problem List saved project migrations for the authenticated account error.
   * @throws ApiError
   */
  public static listProjectMigrations({
    project,
  }: {
    /**
     * Optional exact project slug to filter migration records.
     */
    project?: string,
  }): CancelablePromise<ProjectMigrationList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/project-migrations',
      query: {
        'project': project,
      },
    });
  }
  /**
   * Inspect each reserved deployment and its recorded verification and cutover
   * Inspect each reserved deployment and its recorded verification and cutover. Source-backed HTTP and GraphQL services only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No domain or production traffic changes are performed by this endpoint.
   * @returns ProjectMigration Inspect each reserved deployment and its recorded verification and cutover result.
   * @returns Problem Inspect each reserved deployment and its recorded verification and cutover error.
   * @throws ApiError
   */
  public static getProjectMigration({
    migration,
  }: {
    /**
     * Creator-owned migration journal UUID.
     */
    migration: string,
  }): CancelablePromise<ProjectMigration | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/project-migrations/{migration}',
      path: {
        'migration': migration,
      },
    });
  }
  /**
   * Resume unfinished migration services while preserving accepted deployments
   * Resume unfinished migration services while preserving accepted deployments. Source-backed HTTP and GraphQL services only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No domain or production traffic changes are performed by this endpoint.
   * @returns ProjectMigration Resume unfinished migration services while preserving accepted deployments result.
   * @returns Problem Resume unfinished migration services while preserving accepted deployments error.
   * @throws ApiError
   */
  public static resumeProjectMigration({
    migration,
    requestBody,
  }: {
    /**
     * Saved migration to resume after reviewing unfinished services.
     */
    migration: string,
    requestBody: ResumeProjectMigrationRequest,
  }): CancelablePromise<ProjectMigration | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/project-migrations/{migration}/resume',
      path: {
        'migration': migration,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Deploy an account-owned reviewed project source without another upload.
   * Applies the exact archive retained by a project scan, not the current
   * head of a mutable Git ref. The normal scanner and apply admission gates
   * run again. The account-scoped source expires after one hour; rescan on
   * preview_expired. Initial secrets require secrets:write or admin and are
   * sealed before any deployment is enqueued. Failed configuration starts
   * no builds; saved apps and any completed configuration remain available
   * for correction. Values are never included in responses or scan plans.
   *
   * @returns ApplyResponse Project applied with per-workload deployment receipts.
   * @throws ApiError
   */
  public static applyCachedProjectPlan({
    requestBody,
  }: {
    requestBody: ApplyCachedProjectPlanRequest,
  }): CancelablePromise<ApplyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/apply-plan',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `The caller lacks deployment or secret write permission.`,
        409: `The plan is stale, blocked, or requires environment approval.`,
        410: `The account-owned reviewed source expired; scan again.`,
      },
    });
  }
  /**
   * Scan an uploaded tarball and return a deploy plan.
   * Dry-run. Accepts a multipart upload (`source=<tar.gz>`,
   * `project_slug`, `production_branch`, `install_id`, `only`)
   * and returns a PlanResponse with the discovered workloads,
   * managed services, derived scan_source, and a plan_token
   * that the apply endpoint can echo back to skip the
   * second extract.
   *
   * On over-quota the response carries `can_apply=false`
   * (and `crons_not_allowed=true` for Free plan) so the CLI
   * can branch without a second request.
   *
   * @returns PlanResponse The deploy plan.
   * @throws ApiError
   */
  public static scanProject({
    formData,
    projectAddServices,
    projectStabilization,
    projectProgressive,
    projectWorkerHealth,
    projectReleaseMode,
  }: {
    formData: ProjectScanRequest,
    /**
     * Require planned HTTP source service additions; disabled until native qualification. No removals or configuration changes.
     */
    projectAddServices?: 'required',
    /**
     * Plan a progressive release with ten minutes of monitoring after rollout success.
     */
    projectStabilization?: 'required',
    /**
     * Preview a progressive project rollout with immutable paired configuration and verified health.
     */
    projectProgressive?: 'required',
    /**
     * Require real worker job outcomes in an atomic verified worker release.
     */
    projectWorkerHealth?: 'required',
    /**
     * Set atomic to preview a source-only safe project release against the active production graph.
     */
    projectReleaseMode?: 'atomic' | 'atomic-verify' | 'atomic-config-verify' | 'atomic-migrate-verify' | 'atomic-config-migrate-verify' | 'atomic-verify-workers' | 'atomic-config-verify-workers' | 'atomic-migrate-verify-workers' | 'atomic-config-migrate-verify-workers',
  }): CancelablePromise<PlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/scan',
      headers: {
        'Project-Add-Services': projectAddServices,
        'Project-Stabilization': projectStabilization,
        'Project-Progressive': projectProgressive,
        'Project-Worker-Health': projectWorkerHealth,
        'Project-Release-Mode': projectReleaseMode,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Scan a connected GitHub repository and return a deploy plan.
   * Resolves a durable GitHub App installation owned by the authenticated
   * account, fetches the selected ref through the GitHub integration, and runs the same
   * read-only scanner as the multipart upload endpoint. The installation
   * token remains inside the control plane. When `install_id` is omitted,
   * exactly one connected installation must be able to access `repo`.
   *
   * @returns PlanResponse The deploy plan generated from the fetched repository.
   * @throws ApiError
   */
  public static scanProjectSourceRef({
    requestBody,
  }: {
    requestBody: ProjectSourceRefScanRequest,
  }): CancelablePromise<PlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/scan/source-ref',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read a durable project apply acceptance receipt.
   * Recover the selected workload set and accepted deployment identities for an owned attempt. Reads do not enqueue builds, change configuration, or restart interrupted submissions.
   * @returns ProjectSubmissionReceipt Current project submission and accepted workload identities. Reads never enqueue builds.
   * @throws ApiError
   */
  public static getProjectSubmission({
    attemptId,
  }: {
    /**
     * The opaque project apply attempt recorded before submission.
     */
    attemptId: string,
  }): CancelablePromise<ProjectSubmissionReceipt> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/project-submissions/{attempt_id}',
      path: {
        'attempt_id': attemptId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Control an owned progressive deployment or admit planned schema cleanup.
   * Persist a generation-fenced operator decision. Before full activation abort preserves the unchanged baseline. At full traffic new unassigned work is blocked while fresh baseline health permits paired graph/config restoration. Recovery retries that bounded verification without forcing health or overwriting newer intent. Reusing the same request identity and generation is idempotent. Contract admission additionally requires fresh current health and drained retained versions and work; it permanently fences retired deployments before queuing the source-declared cleanup task.
   * @returns ProjectSubmissionReceipt Durable decision and current receipt; acceptance does not imply restoration has completed.
   * @throws ApiError
   */
  public static controlProjectRelease({
    attemptId,
    action,
    requestBody,
  }: {
    /**
     * Durable project deployment attempt.
     */
    attemptId: string,
    /**
     * Abort a running canary, retry baseline recovery, or run declared schema cleanup after stabilization and drain barriers.
     */
    action: 'abort' | 'recover' | 'contract' | 'resolve-contract',
    requestBody: (ProjectReleaseControlRequest | ProjectMigrationRecoveryRequest),
  }): CancelablePromise<ProjectSubmissionReceipt> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/project-submissions/{attempt_id}/{action}',
      path: {
        'attempt_id': attemptId,
        'action': action,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `Retained rollback baseline expired; deploy a compatible replacement.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Build an existing HTTP project and atomically publish its ready release.
   * Requires an active production release set, the complete project, no_triggers=true, and a durable attempt. Candidates receive zero traffic and publish only after every candidate completes readiness. Project-Add-Services opts into independent HTTP source additions behind native qualification admission. All serving services must remain unchanged. Additions cannot combine with configuration, secrets, migrations, workers or progressive rollout. Verified releases probe existing critical routes and restore the original membership on regression; new services are checked by startup readiness. Failed or withdrawn additions remain quota-counted reservations without a default route and may be retried with identical settings.
   * @returns ApplyResponse Accepted candidate deployments and durable safe release state.
   * @throws ApiError
   */
  public static safeApplyProject({
    deploymentAttemptId,
    formData,
    projectAddServices,
    projectStabilization,
    projectProgressive,
    projectWorkerHealth,
    projectReleaseMode,
    planToken,
  }: {
    /**
     * Durable identity for this safe project submission and release operation.
     */
    deploymentAttemptId: string,
    formData: ProjectApplyRequest,
    /**
     * Apply the scanned independent HTTP service additions after native qualification; preserve existing membership and configuration.
     */
    projectAddServices?: 'required',
    /**
     * Require ten minutes of guarded health monitoring after progressive rollout success.
     */
    projectStabilization?: 'required',
    /**
     * Submit a progressive project rollout behind native qualification admission.
     */
    projectProgressive?: 'required',
    /**
     * Keep the required worker delivery health policy when applying the scanned release.
     */
    projectWorkerHealth?: 'required',
    /**
     * atomic-verify enables durable probes; atomic-config-verify pairs configuration; atomic-migrate-verify and atomic-config-migrate-verify also admit one backward-compatible migration.
     */
    projectReleaseMode?: 'atomic' | 'atomic-verify' | 'atomic-config-verify' | 'atomic-migrate-verify' | 'atomic-config-migrate-verify' | 'atomic-verify-workers' | 'atomic-config-verify-workers' | 'atomic-migrate-verify-workers' | 'atomic-config-migrate-verify-workers',
    /**
     * Scan token bound to the safe release baseline.
     */
    planToken?: string,
  }): CancelablePromise<ApplyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/safe-deploy',
      headers: {
        'Project-Add-Services': projectAddServices,
        'Project-Stabilization': projectStabilization,
        'Project-Progressive': projectProgressive,
        'Project-Worker-Health': projectWorkerHealth,
        'Project-Release-Mode': projectReleaseMode,
        'Deployment-Attempt-ID': deploymentAttemptId,
      },
      query: {
        'plan_token': planToken,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List durable projects owned by the current account.
   * @returns ProjectSummaryResponse Account-scoped project summaries.
   * @throws ApiError
   */
  public static listProjects(): CancelablePromise<Array<ProjectSummaryResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Apply a deploy plan in one transaction.
   * Accepts the same multipart body as /scan plus an optional
   * `plan_token` query parameter echoing the dry-run token. On
   * success the response carries the inserted project_id and
   * per-app IDs so the CLI's `--yes` flow can render
   * `applied: <slug> → <app_id>`. On quota the response is
   * the matching RFC 7807 problem (402 Free crons, 403 apps
   * or cron cap) with zero rows inserted.
   *
   * The apply handler resolves workload-name → app_id from
   * the just-inserted apps and inserts crons in a follow-up
   * pass; the quota check ran inside ApplyProjectPlan's Tx.
   *
   * @returns ApplyResponse The applied project + apps + crons.
   * @throws ApiError
   */
  public static applyProject({
    formData,
    projectRetryTargets,
    deploymentAttemptId,
    planToken,
    idempotencyKey,
  }: {
    formData: ProjectApplyRequest,
    /**
     * JSON object mapping selected app IDs to their latest failed deployment IDs. Requires Deployment-Attempt-ID, exact only selection, no_triggers=true, and no secret or exclusion changes. Forces unchanged workloads to rebuild; rejects stale targets. Acceptance checks the target under the app lock.
     */
    projectRetryTargets?: string,
    /**
     * Durable project submission identity. Reuse only for the same request; inspect its receipt after an uncertain response.
     */
    deploymentAttemptId?: string,
    /**
     * Echo of the dry-run plan_token (base64-JSON). Omit to skip the cache.
     */
    planToken?: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects',
      headers: {
        'Project-Retry-Targets': projectRetryTargets,
        'Deployment-Attempt-ID': deploymentAttemptId,
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'plan_token': planToken,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        409: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Stage encrypted application secret versions for an atomic HTTP release.
   * Requires MFA and secrets write permission. Admission defaults off pending native qualification. Values are encrypted before persistence; the response contains opaque version IDs. Request replay is bounded to the staging window. Existing production keys only; managed credentials are unsupported.
   * @returns ProjectReleaseSecretStageResponse Immutable staged version references, without encrypted or plaintext values.
   * @throws ApiError
   */
  public static stageProjectReleaseSecrets({
    slug,
    requestBody,
  }: {
    /**
     * Existing project slug owning the proposed secret snapshots.
     */
    slug: string,
    requestBody: ProjectReleaseSecretStageRequest,
  }): CancelablePromise<ProjectReleaseSecretStageResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/release-secrets',
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
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Check project deployment or environment promotion readiness without submitting work.
   * Evaluates declared metadata and current account/project state without source
   * uploads, credential staging, probe execution, builds, approvals or writes.
   * Only key names and bounded remedies appear in the report. Status is blocked
   * when a definite blocker exists, otherwise unknown when a required read fails,
   * otherwise ready. Ready applies to control-plane prerequisites; deployment
   * still validates source bytes and runtime health. A state hash is provided
   * only when required state was readable. It is a comparison identity, not
   * an approval or an apply token. Deployment admission remains authoritative.
   *
   * @returns ProjectDeploymentReadiness Current prerequisite report, including blocked and unknown outcomes.
   * @throws ApiError
   */
  public static checkProjectDeployment({
    slug,
    requestBody,
  }: {
    /**
     * Project slug in the authenticated account; a missing project can be checked as a proposed first deployment.
     */
    slug: string,
    requestBody: ProjectDeploymentCheckRequest,
  }): CancelablePromise<ProjectDeploymentReadiness> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/deployment-readiness',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect a project, its workloads, exclusions, and latest status.
   * @returns ProjectResponse Project recovery and workload state.
   * @throws ApiError
   */
  public static getProject({
    slug,
  }: {
    /**
     * Project slug to inspect, update, or delete in the authenticated account.
     */
    slug: string,
  }): CancelablePromise<ProjectResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Update a project's repository binding or production branch.
   * @returns ProjectResponse Updated project state.
   * @throws ApiError
   */
  public static updateProject({
    slug,
    requestBody,
  }: {
    /**
     * Project slug to inspect, update, or delete in the authenticated account.
     */
    slug: string,
    requestBody: UpdateProjectRequest,
  }): CancelablePromise<ProjectResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/projects/{slug}',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete a project and detach its live workloads.
   * The apps remain live. Their project_id is cleared by the database foreign-key action.
   * @returns void
   * @throws ApiError
   */
  public static deleteProject({
    slug,
  }: {
    /**
     * Project slug to inspect, update, or delete in the authenticated account.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Request a complete isolated environment clone.
   * This dedicated route never falls back to partial cloning. Full admission
   * currently returns environment_full_clone_unavailable with named blockers
   * until complete coverage and coordinated data-copy proofs are available.
   *
   * @returns void
   * @throws ApiError
   */
  public static createFullProjectEnvironmentClone({
    slug,
    requestBody,
  }: {
    /**
     * Project whose production or stage environment will be cloned.
     */
    slug: string,
    requestBody: CreateProjectEnvironmentRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environment-clones',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Full-copy capability unavailable; no partial target is created.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read account- and project-scoped durable clone progress.
   * Status is available before the target environment is registered. Private captures, values and worker lease tokens are excluded.
   * @returns ProjectEnvironmentCloneOperationResponse Non-secret clone operation receipt.
   * @throws ApiError
   */
  public static getProjectEnvironmentCloneOperation({
    slug,
    clone,
  }: {
    /**
     * Project owning the durable environment clone operation.
     */
    slug: string,
    /**
     * Durable clone operation identifier returned when capture begins.
     */
    clone: string,
  }): CancelablePromise<ProjectEnvironmentCloneOperationResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environment-clones/{clone}',
      path: {
        'slug': slug,
        'clone': clone,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List durable environments for a project.
   * @returns ProjectEnvironmentResponse Account-scoped project environments.
   * @throws ApiError
   */
  public static listProjectEnvironments({
    slug,
  }: {
    /**
     * Project slug whose environment registry is addressed.
     */
    slug: string,
  }): CancelablePromise<Array<ProjectEnvironmentResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create a durable project environment.
   * When from_environment is supplied, the create is atomic and copies the
   * latest non-secret configuration, runtime variables, and already-sealed
   * customer secrets. Managed PostgreSQL and object-storage bindings receive
   * fresh target-scoped credentials and isolated data by default. Set
   * share_resources to attach fresh credentials to the source resources
   * instead. Provider-issued credential bytes are never copied. Domains,
   * routes, and policies remain application-scoped and are shared.
   *
   * @returns ProjectEnvironmentResponse Project environment created.
   * @throws ApiError
   */
  public static createProjectEnvironment({
    slug,
    requestBody,
  }: {
    /**
     * Project slug whose environment registry is addressed.
     */
    slug: string,
    requestBody: CreateProjectEnvironmentRequest,
  }): CancelablePromise<ProjectEnvironmentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect one project environment.
   * @returns ProjectEnvironmentResponse Project environment.
   * @throws ApiError
   */
  public static getProjectEnvironment({
    slug,
    environment,
  }: {
    /**
     * Project slug owning the environment.
     */
    slug: string,
    /**
     * Environment slug.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}',
      path: {
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Update a project's environment protection policy.
   * @returns ProjectEnvironmentResponse Updated project environment.
   * @throws ApiError
   */
  public static updateProjectEnvironment({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project slug owning the environment.
     */
    slug: string,
    /**
     * Environment slug.
     */
    environment: string,
    requestBody: UpdateProjectEnvironmentRequest,
  }): CancelablePromise<ProjectEnvironmentResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/projects/{slug}/environments/{environment}',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete an unused project environment.
   * Deletes only an unprotected, non-production environment that has no
   * live releases. Configuration, approval history, and owned pending or
   * finished asynchronous work are removed atomically. Production, protected
   * environments, and environments still serving a live release return 409.
   * Running work or an unreleased execution reservation returns 409 with
   * code environment_work_busy; wait for completion or claim recovery and
   * retry. Inconsistent work ownership returns environment_work_ownership_conflict
   * without deleting the environment. Cancellation receipts are retained.
   * If managed resources cannot be revoked immediately, cleanup is durably queued and
   * retried; the deleted environment returns 202 while cleanup is pending.
   *
   * @returns any Project environment deleted; managed-resource cleanup is queued for retry.
   * @throws ApiError
   */
  public static deleteProjectEnvironment({
    slug,
    environment,
    idempotencyKey,
  }: {
    /**
     * Project slug owning the environment.
     */
    slug: string,
    /**
     * Environment slug.
     */
    environment: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect Git source authority and reconciliation history.
   * @returns EnvironmentGitOpsStatusResponse Inspect Git source authority and reconciliation history. result.
   * @throws ApiError
   */
  public static getEnvironmentGitOps({
    slug,
    environment,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
  }): CancelablePromise<EnvironmentGitOpsStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/gitops',
      path: {
        'slug': slug,
        'environment': environment,
      },
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Bind a verified project repository to an environment definition.
   * @returns EnvironmentGitSource Bind a verified project repository to an environment definition. result.
   * @throws ApiError
   */
  public static createEnvironmentGitSource({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Bind a verified project repository to an environment definition. request.
     */
    requestBody: CreateEnvironmentGitSourceRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<EnvironmentGitSource> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/source',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Update report, pruning, and suspension controls. Enforce is unavailable in preview.
   * @returns EnvironmentGitSource Update report, enforce, pruning, and suspension controls. result.
   * @throws ApiError
   */
  public static updateEnvironmentGitSource({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Update report, enforce, pruning, and suspension controls. request.
     */
    requestBody: EnvironmentGitSourceUpdate,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<EnvironmentGitSource> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/source',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Release Git ownership while preserving values and retired binding history.
   * @returns void
   * @throws ApiError
   */
  public static detachEnvironmentGitSource({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    requestBody: DetachEnvironmentGitSourceRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/source',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Retire the binding and create a verified report source requiring fresh approval.
   * @returns EnvironmentGitSource Replacement source requiring a new definition review and ownership adoption.
   * @throws ApiError
   */
  public static rebindEnvironmentGitSource({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    requestBody: RebindEnvironmentGitSourceRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<EnvironmentGitSource> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/source/rebind',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Reserve scoped Terraform variable, source or configuration fields against Git adoption.
   * @returns EnvironmentFieldOwnershipResponse Scoped fields were reserved, or the request targets legacy unscoped intent.
   * @throws ApiError
   */
  public static claimEnvironmentFieldOwnership({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: EnvironmentFieldOwnershipRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<EnvironmentFieldOwnershipResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/environment-field-ownership',
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Release scoped Terraform field ownership without changing values.
   * @returns EnvironmentFieldOwnershipResponse The specified ownership claims were released; values were preserved.
   * @throws ApiError
   */
  public static releaseEnvironmentFieldOwnership({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: EnvironmentFieldOwnershipRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<EnvironmentFieldOwnershipResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/environment-field-ownership',
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Preview field changes for an immutable commit without approval or writes.
   * Requires environment read scope and a bound Git source. Verifies the immutable commit, repository identity and definition scope, then observes current intent and ownership. Values are redacted. The candidate plan cannot approve, adopt, deploy or certify runtime readiness. Request a fresh plan after source or environment changes.
   * @returns PreviewEnvironmentGitPlanResponse Read-only field plan with redacted values and current observation metadata.
   * @throws ApiError
   */
  public static previewEnvironmentGitPlan({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Immutable commit to assess using the bound Git source.
     */
    requestBody: PreviewEnvironmentGitRevisionRequest,
  }): CancelablePromise<PreviewEnvironmentGitPlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/plan',
      path: {
        'slug': slug,
        'environment': environment,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Preview image promotion from a proven serving environment release.
   * Read-only, value-redacted proposal. Reads both bound sources, source release, current graph evidence, artifacts, target intent and ownership on one snapshot. Source builds without a portable artifact reference are blocked. The hash grants no approval or execution authority; expected_plan_hash rejects changed observations. Target review and fresh qualification remain required.
   * @returns PreviewEnvironmentGitPromotionResponse Read-only promotion proposal; blockers prevent patch and manifest export.
   * @throws ApiError
   */
  public static previewEnvironmentGitPromotion({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Source environment, immutable target-manifest commit, and optional previously reviewed observation hash.
     */
    requestBody: PreviewEnvironmentGitPromotionRequest,
  }): CancelablePromise<PreviewEnvironmentGitPromotionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/promotion-plan',
      path: {
        'slug': slug,
        'environment': environment,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List retained GitOps serving releases and current rollback eligibility.
   * Verifies the immutable target manifest commit through the bound provider and assesses each release using the rollback planner on one scoped observation snapshot per page. Only completed serving history of the current source is listed. Eligibility can change after observation. Release compatibility deadlines do not guarantee artifact retention. This read-only catalog never pins artifacts or authorizes rollback execution.
   * @returns ListEnvironmentGitReleasesResponse Bounded recovery catalog with current eligibility and public artifact identities.
   * @throws ApiError
   */
  public static listEnvironmentGitReleases({
    slug,
    environment,
    targetCommitSha,
    before,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Immutable commit containing the target manifest whose configuration will be preserved.
     */
    targetCommitSha: string,
    /**
     * next_before release UUID from the previous recovery page in this Git source scope.
     */
    before?: string,
  }): CancelablePromise<ListEnvironmentGitReleasesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/releases',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'target_commit_sha': targetCommitSha,
        'before': before,
      },
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Propose a code rollback from a retained GitOps serving release.
   * Read-only, value-redacted proposal selecting exact retained artifacts in this environment. Rechecks historical serving evidence, current artifact trust, identity mappings, target intent and ownership on one snapshot. It preserves current configuration and changes only compatible workload sources. Review the observation hash before export; normal Git approval and fresh qualification remain required.
   * @returns PreviewEnvironmentGitRollbackResponse Read-only rollback proposal; blockers prevent patch and manifest export.
   * @throws ApiError
   */
  public static previewEnvironmentGitRollback({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Retained release UUID, immutable target-manifest commit, and optional previously reviewed observation hash.
     */
    requestBody: PreviewEnvironmentGitRollbackRequest,
  }): CancelablePromise<PreviewEnvironmentGitRollbackResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/rollback-plan',
      path: {
        'slug': slug,
        'environment': environment,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Fetch an immutable commit and review its definition digest.
   * @returns PreviewEnvironmentGitRevisionResponse Fetch an immutable commit and review its definition digest. result.
   * @throws ApiError
   */
  public static previewEnvironmentGitRevision({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Fetch an immutable commit and review its definition digest. request.
     */
    requestBody: PreviewEnvironmentGitRevisionRequest,
  }): CancelablePromise<PreviewEnvironmentGitRevisionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/revisions/preview',
      path: {
        'slug': slug,
        'environment': environment,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Approve reviewed Git bytes and enqueue durable reconciliation.
   * @returns ApproveEnvironmentGitRevisionResponse Approve reviewed Git bytes and enqueue durable reconciliation. result.
   * @throws ApiError
   */
  public static approveEnvironmentGitRevision({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Approve reviewed Git bytes and enqueue durable reconciliation. request.
     */
    requestBody: ApproveEnvironmentGitRevisionRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApproveEnvironmentGitRevisionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/revisions/approve',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Preview adoption without changing ownership or resource values.
   * @returns EnvironmentGitOpsPlan Preview adoption without changing ownership or resource values. result.
   * @throws ApiError
   */
  public static previewEnvironmentGitOpsAdoption({
    slug,
    environment,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
  }): CancelablePromise<EnvironmentGitOpsPlan> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/adoption-preview',
      path: {
        'slug': slug,
        'environment': environment,
      },
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Adopt fields from a current reviewed plan while preserving their values.
   * @returns AdoptEnvironmentGitOpsResponse Adopt fields from a current reviewed plan while preserving their values. result.
   * @throws ApiError
   */
  public static adoptEnvironmentGitOps({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Adopt fields from a current reviewed plan while preserving their values. request.
     */
    requestBody: AdoptEnvironmentGitOpsRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AdoptEnvironmentGitOpsResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/adopt',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Permit a temporary edit to one Git-owned field.
   * @returns EnvironmentGitOpsOverrideRequest Permit a temporary edit to one Git-owned field. result.
   * @throws ApiError
   */
  public static createEnvironmentGitOpsOverride({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Permit a temporary edit to one Git-owned field. request.
     */
    requestBody: EnvironmentGitOpsOverrideRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<EnvironmentGitOpsOverrideRequest> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/overrides',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Revoke a temporary field override and enqueue reconciliation.
   * @returns void
   * @throws ApiError
   */
  public static removeEnvironmentGitOpsOverride({
    slug,
    environment,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Project owning the environment Git authority.
     */
    slug: string,
    /**
     * Registered environment selected for Git intent management.
     */
    environment: string,
    /**
     * Revoke a temporary field override and enqueue reconciliation. request.
     */
    requestBody: RemoveEnvironmentGitOpsOverrideRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/gitops/overrides',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Get the latest non-secret environment configuration.
   * @returns ProjectEnvironmentConfigResponse Latest immutable configuration version, or the implicit empty configuration.
   * @throws ApiError
   */
  public static getProjectEnvironmentConfig({
    slug,
    environment,
  }: {
    /**
     * Project slug owning the environment configuration.
     */
    slug: string,
    /**
     * Environment slug whose configuration is addressed.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentConfigResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/config',
      path: {
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Append a non-secret environment configuration version.
   * Secret-shaped keys are rejected; sensitive values belong in the secrets surface.
   * @returns ProjectEnvironmentConfigResponse Stored immutable configuration version.
   * @throws ApiError
   */
  public static updateProjectEnvironmentConfig({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project slug owning the environment configuration.
     */
    slug: string,
    /**
     * Environment slug whose configuration is addressed.
     */
    environment: string,
    requestBody: UpdateProjectEnvironmentConfigRequest,
  }): CancelablePromise<ProjectEnvironmentConfigResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/config',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the secrets shared by every service of a project environment.
   * Metadata only: key, version, sealing key id and value
   * fingerprint. Values are write-only and never returned.
   *
   * @returns ProjectEnvironmentSharedSecretsResponse The environment's shared secrets and the plan's limit.
   * @throws ApiError
   */
  public static listProjectEnvironmentSecrets({
    slug,
    environment,
  }: {
    /**
     * Project slug whose environment shares the listed secrets.
     */
    slug: string,
    /**
     * Environment whose shared secrets are addressed.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentSharedSecretsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/secrets',
      path: {
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Set a secret shared by every service of a project environment.
   * Seals the value for the fleet and stores it once for the environment. Each service receives it at its next boot unless it sets
   * the same key itself; services' snapshots in the environment are
   * invalidated. Platform identity keys are rejected.
   *
   * @returns ProjectEnvironmentSharedSecretResponse The stored secret's metadata.
   * @throws ApiError
   */
  public static setProjectEnvironmentSecret({
    slug,
    environment,
    key,
    requestBody,
  }: {
    /**
     * Project slug whose environment shares the secret.
     */
    slug: string,
    /**
     * Environment whose shared secret is addressed.
     */
    environment: string,
    /**
     * Secret key. Must start with a letter; A-Z, 0-9, underscore.
     */
    key: string,
    requestBody: SetProjectEnvironmentSharedSecretRequest,
  }): CancelablePromise<ProjectEnvironmentSharedSecretResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/secrets/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_project_secrets`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: secret_value_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete a secret shared by a project environment.
   * Services stop receiving it at their next boot.
   * @returns void
   * @throws ApiError
   */
  public static deleteProjectEnvironmentSecret({
    slug,
    environment,
    key,
  }: {
    /**
     * Project slug whose environment shares the secret.
     */
    slug: string,
    /**
     * Environment whose shared secret is addressed.
     */
    environment: string,
    /**
     * Secret key. Must start with a letter; A-Z, 0-9, underscore.
     */
    key: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/secrets/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replace the value of an existing shared secret.
   * Like PUT, but an unknown key is 404 instead of being created.
   * @returns ProjectEnvironmentSharedSecretResponse The rotated secret's metadata, with its new version.
   * @throws ApiError
   */
  public static rotateProjectEnvironmentSecret({
    slug,
    environment,
    key,
    requestBody,
  }: {
    /**
     * Project slug whose environment shares the rotated secret.
     */
    slug: string,
    /**
     * Environment whose shared secret is rotated.
     */
    environment: string,
    /**
     * Secret key. Must start with a letter; A-Z, 0-9, underscore.
     */
    key: string,
    requestBody: SetProjectEnvironmentSharedSecretRequest,
  }): CancelablePromise<ProjectEnvironmentSharedSecretResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/secrets/{key}/rotate',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: secret_value_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get current live releases for a project environment.
   * Returns one non-secret release record for every project workload.
   * Workloads without a live deployment are returned with
   * `status=not_deployed`. Configuration values and secret material are
   * never included.
   *
   * @returns ProjectEnvironmentReleaseListResponse Current environment release inventory.
   * @throws ApiError
   */
  public static getProjectEnvironmentReleases({
    slug,
    environment,
  }: {
    /**
     * Project slug owning the environment release inventory.
     */
    slug: string,
    /**
     * Environment whose current live releases are requested.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentReleaseListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/releases',
      path: {
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List active, retired, and expired project release sets.
   * Returns newest first, ordered by created_at and ID descending. Pass next_before as before for the next page.
   * @returns ProjectReleaseSetListResponse Page of release graphs owned by the requested project environment.
   * @throws ApiError
   */
  public static listProjectReleaseSets({
    slug,
    environment,
    before,
    limit = 50,
  }: {
    /**
     * Project slug owning the release set.
     */
    slug: string,
    /**
     * Target project environment.
     */
    environment: string,
    /**
     * Continue release-set history from the next_before cursor returned by the preceding page.
     */
    before?: string,
    /**
     * Maximum number of release sets to return.
     */
    limit?: number,
  }): CancelablePromise<ProjectReleaseSetListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Atomically activate an immutable project deployment graph.
   * Every project workload must have one live deployment and revision pinning enabled for at least the requested TTL. Previous release sets remain addressable until expiry.
   * @returns ProjectReleaseSetResponse Published release set.
   * @throws ApiError
   */
  public static publishProjectReleaseSet({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project slug owning the release set.
     */
    slug: string,
    /**
     * Target project environment.
     */
    environment: string,
    requestBody: PublishProjectReleaseSetRequest,
  }): CancelablePromise<ProjectReleaseSetResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Record health and smoke results for an active release set.
   * Records a closed-schema qualification receipt for the exact active
   * release-set ID, non-secret source configuration version/hash, and
   * per-workload secret revision fingerprints. Fingerprints include only
   * revision metadata and managed credential generations, never secret
   * values or value hashes.
   * Each health and smoke result identifies every workload's exact
   * deployment and contains only its HTTP status or a bounded error code;
   * response bodies and secrets are never stored. The API rejects probes
   * if the release set, configuration, or secret revisions change before
   * receipt creation.
   * Receipts expire after 24 hours and cannot qualify a later release set
   * or configuration or secret revision.
   *
   * @returns ProjectEnvironmentQualificationResponse Qualification receipt, expiring 24 hours after creation.
   * @throws ApiError
   */
  public static createProjectEnvironmentQualification({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project whose source environment release is being qualified.
     */
    slug: string,
    /**
     * Source environment containing the exact active release set.
     */
    environment: string,
    requestBody: CreateProjectEnvironmentQualificationRequest,
  }): CancelablePromise<ProjectEnvironmentQualificationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/qualifications',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get the active project release graph.
   * Returns 404 when no active release exists in this project environment.
   * @returns ProjectReleaseSetResponse Currently active release graph and its immutable deployment membership.
   * @throws ApiError
   */
  public static getActiveProjectReleaseSet({
    slug,
    environment,
  }: {
    /**
     * Project whose active release is requested.
     */
    slug: string,
    /**
     * Environment whose active graph should be returned.
     */
    environment: string,
  }): CancelablePromise<ProjectReleaseSetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/active',
      path: {
        'slug': slug,
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect a specific project release graph.
   * Includes retired and expired graphs for diagnosis; this read does not make them routable.
   * @returns ProjectReleaseSetResponse Requested historical release graph, including its activation and expiry metadata.
   * @throws ApiError
   */
  public static getProjectReleaseSet({
    slug,
    environment,
    release,
  }: {
    /**
     * Project owning the specific release being inspected.
     */
    slug: string,
    /**
     * Environment that originally published the requested release.
     */
    environment: string,
    /**
     * Release-set UUID.
     */
    release: string,
  }): CancelablePromise<ProjectReleaseSetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/{release}',
      path: {
        'slug': slug,
        'environment': environment,
        'release': release,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get effective state for a project environment.
   * Returns configuration, live releases, non-secret runtime variables,
   * secret fingerprints, and managed binding metadata. Secret plaintext,
   * ciphertext, and sealing-key identifiers are never returned. Resources
   * that remain application-scoped are identified under shared_resources.
   *
   * @returns ProjectEnvironmentStateResponse Effective environment state.
   * @throws ApiError
   */
  public static getProjectEnvironmentState({
    slug,
    environment,
  }: {
    /**
     * Project whose environment state is requested.
     */
    slug: string,
    /**
     * Target environment whose effective state is returned.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentStateResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/state',
      path: {
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replace a workload's declared-route contract in one environment.
   * Scoped deployment URLs use this contract; the ordinary application hostname retains its application-wide contract.
   * @returns ProjectEnvironmentRoutePolicyResponse Stored environment-owned route contract.
   * @throws ApiError
   */
  public static updateProjectEnvironmentRoutes({
    slug,
    environment,
    workload,
    requestBody,
  }: {
    /**
     * Project owning the environment.
     */
    slug: string,
    /**
     * Registered environment whose route contract is replaced.
     */
    environment: string,
    /**
     * Workload application slug in the project.
     */
    workload: string,
    requestBody: UpdateProjectEnvironmentRoutePolicyRequest,
  }): CancelablePromise<ProjectEnvironmentRoutePolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/workloads/{workload}/routes',
      path: {
        'slug': slug,
        'environment': environment,
        'workload': workload,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replace headers and CORS rules for a workload in one environment.
   * An explicit empty list disables inherited headers/CORS rules on the stable environment URL. Other edge-rule kinds and ordinary application hosts are unchanged.
   * @returns ProjectEnvironmentEdgePolicyResponse Stored environment-owned headers/CORS policy.
   * @throws ApiError
   */
  public static updateProjectEnvironmentPolicies({
    slug,
    environment,
    workload,
    requestBody,
  }: {
    /**
     * Project containing the environment-specific edge policy.
     */
    slug: string,
    /**
     * Registered environment whose headers/CORS policy is replaced.
     */
    environment: string,
    /**
     * Workload slug whose environment-specific edge policy is replaced.
     */
    workload: string,
    requestBody: UpdateProjectEnvironmentEdgePolicyRequest,
  }): CancelablePromise<ProjectEnvironmentEdgePolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/workloads/{workload}/policies',
      path: {
        'slug': slug,
        'environment': environment,
        'workload': workload,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a stage workload's complete desired queue configuration.
   * Returns logical definitions and the current workload revision. A missing
   * collection returns 409 environment_queue_collection_unavailable and never
   * inherits production queues. X-Gregale-Workload-Revision identifies the
   * head to use when initializing or replacing the collection.
   * Consumer activation is currently unavailable; saved definitions do not
   * activate delivery or qualify the stage for promotion.
   *
   * @returns ProjectEnvironmentQueueBindingsResponse Complete stage-owned desired queue collection.
   * @throws ApiError
   */
  public static getProjectEnvironmentQueueBindings({
    slug,
    environment,
    workload,
  }: {
    /**
     * Project containing the workload and stage.
     */
    slug: string,
    /**
     * Registered stage. Production is managed through the app queue-bindings API.
     */
    environment: string,
    /**
     * Workload application slug belonging to this project.
     */
    workload: string,
  }): CancelablePromise<ProjectEnvironmentQueueBindingsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings',
      path: {
        'slug': slug,
        'environment': environment,
        'workload': workload,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replace a stage workload's complete desired queue configuration.
   * Requires expected_revision to match the complete workload settings head,
   * with zero for an uninitialized workload. An explicit empty bindings list
   * removes all stage definitions. Stale revisions and protected stages
   * return 409, including protected no-op writes. Definitions have logical
   * names and no production consumer IDs. Existing deployments retain their
   * pinned definitions. Consumer activation is currently unavailable.
   *
   * @returns ProjectEnvironmentQueueBindingsResponse Stored desired queue definitions and their workload revision.
   * @throws ApiError
   */
  public static replaceProjectEnvironmentQueueBindings({
    slug,
    environment,
    workload,
    requestBody,
  }: {
    /**
     * Project containing the workload and stage.
     */
    slug: string,
    /**
     * Registered stage. Production is managed through the app queue-bindings API.
     */
    environment: string,
    /**
     * Workload application slug belonging to this project.
     */
    workload: string,
    requestBody: ReplaceProjectEnvironmentQueueBindingsRequest,
  }): CancelablePromise<ProjectEnvironmentQueueBindingsResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings',
      path: {
        'slug': slug,
        'environment': environment,
        'workload': workload,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Compare two effective project environments.
   * Compares configuration, releases, runtime variables, secret
   * fingerprints and credential generations, and managed bindings. Secret
   * values are never returned. A secret without a fingerprint is reported
   * as unknown rather than incorrectly reported as equal.
   *
   * @returns ProjectEnvironmentDiffResponse Unified effective-state diff.
   * @throws ApiError
   */
  public static getProjectEnvironmentDiff({
    slug,
    environment,
    from,
  }: {
    /**
     * Project slug whose environments are compared.
     */
    slug: string,
    /**
     * Target environment.
     */
    environment: string,
    /**
     * Source environment being compared with the target environment.
     */
    from: string,
  }): CancelablePromise<ProjectEnvironmentDiffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/diff',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'from': from,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Compare two environment configuration snapshots.
   * @returns ProjectEnvironmentConfigDiffResponse Stable key-level configuration diff ordered by key.
   * @throws ApiError
   */
  public static getProjectEnvironmentConfigDiff({
    slug,
    environment,
    from,
  }: {
    /**
     * Project slug whose environment configurations are compared.
     */
    slug: string,
    /**
     * Destination environment for the configuration comparison.
     */
    environment: string,
    /**
     * Baseline environment for the configuration snapshot comparison.
     */
    from: string,
  }): CancelablePromise<ProjectEnvironmentConfigDiffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/config/diff',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'from': from,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Request signed-in human review of an exact protected promotion.
   * @returns BrowserPromotionApprovalResponse Private bridge response; credentials must never be returned to an agent.
   * @throws ApiError
   */
  public static requestBrowserPromotionApproval({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project slug for promotion browser approval request.
     */
    slug: string,
    /**
     * Target registered environment slug for promotion browser approval request.
     */
    environment: string,
    requestBody: BrowserPromotionApprovalRequest,
  }): CancelablePromise<BrowserPromotionApprovalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/approval-requests',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Privately poll a browser-approved promotion; retry returns the same unconsumed credential.
   * @returns ProjectEnvironmentApprovalResponse Existing promotion approval bound to the reviewed plan, after human authorization.
   * @throws ApiError
   */
  public static exchangeBrowserPromotionApproval({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project slug for authorized promotion ticket exchange.
     */
    slug: string,
    /**
     * Target registered environment slug for authorized promotion ticket exchange.
     */
    environment: string,
    requestBody: BrowserPromotionApprovalExchange,
  }): CancelablePromise<ProjectEnvironmentApprovalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/approval-requests/exchange',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Approve one exact plan for a protected project environment.
   * @returns ProjectEnvironmentApprovalResponse Short-lived approval credential.
   * @throws ApiError
   */
  public static approveProjectEnvironment({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project slug owning the protected environment.
     */
    slug: string,
    /**
     * Protected environment slug to approve.
     */
    environment: string,
    requestBody: CreateProjectEnvironmentApprovalRequest,
  }): CancelablePromise<ProjectEnvironmentApprovalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/approvals',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get the lifecycle status of a project environment approval.
   * The raw approval token is never returned by this endpoint.
   * @returns ProjectEnvironmentApprovalStatusResponse Durable approval lifecycle status.
   * @throws ApiError
   */
  public static getProjectEnvironmentApprovalStatus({
    slug,
    environment,
    approval,
  }: {
    /**
     * Project slug owning the approval lookup.
     */
    slug: string,
    /**
     * Protected environment slug containing the approval.
     */
    environment: string,
    /**
     * Durable approval identifier returned when the approval is created.
     */
    approval: string,
  }): CancelablePromise<ProjectEnvironmentApprovalStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/approvals/{approval}',
      path: {
        'slug': slug,
        'environment': environment,
        'approval': approval,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Preview promotion of live workloads between project environments.
   * Read-only comparison of the source and target environment. The
   * response includes non-secret configuration changes, live deployment
   * identities, target protection state, and an opaque promotion token
   * bound to those identities. It does not create deployments or audit
   * mutations. Configuration remains target-scoped by default;
   * `sync_config=true` opts into applying the source's non-secret
   * configuration snapshot with the release graph, and is blocked unless
   * the target already has an active release graph for atomic cutover.
   * Protected-target promotions from an active source release set also
   * require the latest passing health and smoke qualification, which is
   * bound to that immutable release-set ID and expires after 24 hours.
   * With `verify=true`, the exact GET/HEAD critical probe policy is also
   * captured in the approval identity. The first milestone requires pinned
   * HTTP configuration, a probe for every HTTP workload, at most five probes,
   * and at least 21 minutes of revision retention. With `workers=true`, one
   * existing production push worker participates in the same cutover. Its
   * destination binding and credentials remain unchanged; startup proof and
   * real delivery health gate promotion.
   *
   * @returns ProjectEnvironmentPromotionPreviewResponse Promotion preview and immutable promotion identity.
   * @throws ApiError
   */
  public static getProjectEnvironmentPromotionPreview({
    slug,
    environment,
    from,
    syncConfig = false,
    verify = false,
    migrate = false,
    workers = false,
  }: {
    /**
     * Project slug owning the environment promotion preview.
     */
    slug: string,
    /**
     * Target environment receiving the promotion preview.
     */
    environment: string,
    /**
     * Source environment whose live releases are compared with the target.
     */
    from: string,
    /**
     * Opt in to copying the source's non-secret configuration atomically with the target release graph.
     */
    syncConfig?: boolean,
    /**
     * Require destination HTTP probes before cutover and success; requires snapshot-aware sync_config and qualified operator admission.
     */
    verify?: boolean,
    /**
     * Run exactly one source release command as a backward-compatible destination migration before health probes. Requires sync_config, verify and qualified migration admission. Schema changes remain after code/config rollback.
     */
    migrate?: boolean,
    /**
     * Promote HTTP services and one existing production push worker with coordinated job admission and delivery health. Requires sync_config and verify; migration combinations are unsupported.
     */
    workers?: boolean,
  }): CancelablePromise<ProjectEnvironmentPromotionPreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/promotion-preview',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'from': from,
        'sync_config': syncConfig,
        'verify': verify,
        'migrate': migrate,
        'workers': workers,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Preview promotion with destination production secret changes.
   * Read-only, bounded preview containing immutable secret version references
   * from the authenticated release-secrets staging endpoint. Requires MFA,
   * secrets write permission, sync_config, verify, complete HTTP release graphs
   * and production as the destination. Existing application-owned persistent
   * secret keys only; workers, migrations, managed credentials, explicit grants
   * and reload hooks are refused. Each mentioned workload supplies a complete
   * secret map; an empty map removes its serving secrets and omitted workloads
   * inherit production versions. Source secrets are never copied. Approval and
   * execution revalidate the exact versions and destination baseline. New
   * admissions also require qualified operator rollout for the secrets scope.
   *
   * @returns ProjectEnvironmentPromotionPreviewResponse Promotion preview with secret key changes and version references only.
   * @throws ApiError
   */
  public static previewProjectEnvironmentPromotionSecrets({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project slug owning the environment promotion preview.
     */
    slug: string,
    /**
     * Target environment receiving the promotion preview.
     */
    environment: string,
    requestBody: ProjectEnvironmentPromotionSecretPreviewRequest,
  }): CancelablePromise<ProjectEnvironmentPromotionPreviewResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/promotion-preview',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Secret releases or destination verification are unavailable.`,
      },
    });
  }
  /**
   * Execute a guarded promotion between project environments.
   * Revalidates the supplied promotion token against current live
   * deployments and environment configuration before promoting immutable
   * source artifacts. Protected targets also require an approval token
   * issued for that exact promotion. After cutover, every target artifact
   * is verified against its source release and target environment. A
   * failed verification automatically rolls back the promotion. Target
   * configuration remains target-scoped unless the token was created with
   * `sync_config=true`; secrets are never copied from the source environment.
   * A health-gated promotion returns 202 with status=running. A leased worker
   * checks dark candidates in the destination before atomic cutover, then
   * requires fresh post-cutover health. Check the promotion status endpoint
   * for phase, evidence, deadline and any guarded automatic rollback.
   * Excess verified promotions wait durably with health.phase=queued and an
   * admission receipt for at most 15 minutes. Dispatch revalidates the approved
   * intent before preparing workloads and starts a fresh health window.
   * A full waiting queue returns 429 (project_promotion_queue_full) with
   * Retry-After; no operation is accepted and approval remains available.
   *
   * @returns ProjectEnvironmentPromotionResponse Promotion result for each project workload.
   * @throws ApiError
   */
  public static promoteProjectEnvironment({
    slug,
    environment,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Project slug owning the environment promotion.
     */
    slug: string,
    /**
     * Target environment receiving the promotion.
     */
    environment: string,
    /**
     * Stable key for retrying this promotion without duplicating deployments.
     */
    idempotencyKey: string,
    requestBody: PromoteProjectEnvironmentRequest,
  }): CancelablePromise<ProjectEnvironmentPromotionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/promote',
      path: {
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List project environment promotion history.
   * Returns newest-first compact promotion history. Use the promotion status endpoint for per-workload checkpoints.
   * @returns ProjectEnvironmentPromotionListResponse Cursor-paginated promotion history.
   * @throws ApiError
   */
  public static listProjectEnvironmentPromotions({
    slug,
    environment,
    before,
    limit = 50,
    from,
    status,
  }: {
    /**
     * Project slug owning the promotion history.
     */
    slug: string,
    /**
     * Target environment whose promotion history is requested.
     */
    environment: string,
    /**
     * Opaque cursor returned as next_before.
     */
    before?: string,
    /**
     * Page size, capped at 100.
     */
    limit?: number,
    /**
     * Restrict history to promotions from this source environment.
     */
    from?: string,
    /**
     * Restrict history to a promotion lifecycle status.
     */
    status?: 'running' | 'succeeded' | 'failed',
  }): CancelablePromise<ProjectEnvironmentPromotionListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/promotions',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'before': before,
        'limit': limit,
        'from': from,
        'status': status,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get durable project environment promotion status.
   * Returns the operation status, post-cutover verification result, and non-secret per-workload checkpoints for a promotion.
   * @returns ProjectEnvironmentPromotionStatusResponse Durable promotion status, including post-cutover verification and any automatic rollback.
   * @throws ApiError
   */
  public static getProjectEnvironmentPromotionStatus({
    slug,
    environment,
    promotion,
  }: {
    /**
     * Project slug owning the promotion status operation.
     */
    slug: string,
    /**
     * Target environment associated with the promotion status operation.
     */
    environment: string,
    /**
     * Durable promotion operation identifier.
     */
    promotion: string,
  }): CancelablePromise<ProjectEnvironmentPromotionStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/promotions/{promotion}',
      path: {
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Abort an in-progress environment promotion.
   * A durable generation-fenced operator decision. Before cutover, abort preserves destination intent and retires unused candidates. After cutover, abort or recover requires fresh server-owned baseline health and worker startup proof before atomic restoration. Recovery never rebuilds or extends retention. Migration incidents use recover-migration. Identical request replay returns that decision's durable receipt even after a newer decision.
   * @returns ProjectPromotionControl Terminal operator decision acknowledgement.
   * @throws ApiError
   */
  public static abortProjectEnvironmentPromotion({
    slug,
    environment,
    promotion,
    requestBody,
  }: {
    /**
     * Project slug owning the abort request.
     */
    slug: string,
    /**
     * Destination environment of the promotion being aborted.
     */
    environment: string,
    /**
     * Durable promotion identifier to abort.
     */
    promotion: string,
    requestBody: ProjectPromotionControlRequest,
  }): CancelablePromise<ProjectPromotionControl> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/promotions/{promotion}/abort',
      path: {
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Stale generation, reused request identity, unsupported phase, or changed destination intent; inspect promotion status.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Retry guarded restoration of a blocked environment promotion.
   * Retry a blocked restoration with a generation-fenced operator decision. Fresh server-owned baseline health and worker startup proof are required before atomic restoration. The retained baseline is reused without rebuilding or extending retention; migration incidents require recover-migration. An identical request replays its original durable receipt even after a newer decision.
   * @returns ProjectPromotionControl Terminal acknowledgement of the restoration recovery decision.
   * @throws ApiError
   */
  public static recoverProjectEnvironmentPromotion({
    slug,
    environment,
    promotion,
    requestBody,
  }: {
    /**
     * Project slug owning the restoration recovery request.
     */
    slug: string,
    /**
     * Destination environment receiving the restored baseline.
     */
    environment: string,
    /**
     * Durable promotion identifier whose blocked restoration is being retried.
     */
    promotion: string,
    requestBody: ProjectPromotionControlRequest,
  }): CancelablePromise<ProjectPromotionControl> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/promotions/{promotion}/recover',
      path: {
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Recovery rejected because the inspected generation, request identity, phase or destination intent no longer permits restoration.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Recover an interrupted promotion migration without replaying SQL.
   * Records explicit database inspection, requests scheduler-confirmed teardown of the original runtime, and either resumes the captured promotion's health checks or resolves the incident through a different verified active repair promotion. Original task outcomes remain unchanged. Same-request replay returns the durable acknowledgement.
   * @returns ProjectPromotionMigrationRecovery Existing terminal recovery acknowledgement.
   * @throws ApiError
   */
  public static recoverProjectEnvironmentPromotionMigration({
    slug,
    environment,
    promotion,
    requestBody,
  }: {
    /**
     * Project slug owning the migration recovery request.
     */
    slug: string,
    /**
     * Destination environment whose migration was inspected.
     */
    environment: string,
    /**
     * Durable promotion identifier with the interrupted migration task.
     */
    promotion: string,
    requestBody: ProjectPromotionMigrationRecoveryRequest,
  }): CancelablePromise<ProjectPromotionMigrationRecovery> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/promotions/{promotion}/recover-migration',
      path: {
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Task, generation, runtime or destination intent changed; inspect current promotion status.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Roll back a completed or failed project environment promotion.
   * Restores each workload to the target deployment captured before the
   * promotion. Workloads created by the promotion are failed and removed
   * from traffic when there was no prior target. The operation refuses to
   * overwrite a target changed after the promotion and records durable
   * per-workload rollback checkpoints.
   *
   * @returns ProjectEnvironmentPromotionStatusResponse Durable rollback status and per-workload restoration result.
   * @throws ApiError
   */
  public static rollbackProjectEnvironmentPromotion({
    slug,
    environment,
    promotion,
    idempotencyKey,
  }: {
    /**
     * Project slug owning the promotion rollback operation.
     */
    slug: string,
    /**
     * Target environment receiving the promotion rollback.
     */
    environment: string,
    /**
     * Durable promotion operation identifier to roll back.
     */
    promotion: string,
    /**
     * Stable key that makes this rollback replayable without repeating cutover.
     */
    idempotencyKey: string,
  }): CancelablePromise<ProjectEnvironmentPromotionStatusResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/promotions/{promotion}/rollback',
      path: {
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Preview the workloads and related state affected by project deletion.
   * @returns ProjectDeletePreviewResponse Deletion impact. Workloads and their related resources remain live after detachment.
   * @throws ApiError
   */
  public static previewDeleteProject({
    slug,
  }: {
    /**
     * Project slug whose deletion impact should be previewed.
     */
    slug: string,
  }): CancelablePromise<ProjectDeletePreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/delete-preview',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Drop a persisted --exclude row from deployment_scope_exclusions.
   * Operator escape hatch (code-review fix #2) for
   * when a persisted slug no longer exists in the repo
   * (workload was renamed or deleted) and is blocking
   * subsequent deploys via exclude_unknown_slug. Without
   * this endpoint the only option was psql + hand-DELETE;
   * the CLI's `gregale deployments exclude clear
   * --slug=NAME --project-slug=SLUG` calls into here as the
   * operator-grade path. Idempotent — DELETE on no row
   * returns 404 scope_exclusion_not_found so the CLI can
   * render "already clear" without surfacing a hard error.
   *
   * @returns any The exclusion was cleared.
   * @throws ApiError
   */
  public static deleteDeploymentScopeExclusion({
    slug,
    slug2,
  }: {
    /**
     * Project slug (the (account, project) namespace owning the persisted exclusion).
     */
    slug: string,
    /**
     * Excluded workload slug (the app slug persisted via a prior --persist-exclude deploy).
     */
    slug2: string,
  }): CancelablePromise<{
    ok?: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/exclusions/{slug2}',
      path: {
        'slug': slug,
        'slug2': slug2,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Either the project does not exist or no persisted
        exclusion matches the slug. Both surface as
        scope_exclusion_not_found so the existence of a
        project is not leaked via the operator surface.
        `,
      },
    });
  }
  /**
   * Explain verification blockers and safe public changes without admitting work
   * @returns ProjectBindingVerificationRunDiagnostics Read-only public observations and suggested recovery; grants no admission or activation authority.
   * @throws ApiError
   */
  public static getProjectBindingVerificationRunDiagnostics({
    slug,
    environment,
    run,
  }: {
    /**
     * Slug of the project for diagnosing blockers in a project verification graph.
     */
    slug: string,
    /**
     * Project environment for diagnosing blockers in a project verification graph.
     */
    environment: string,
    /**
     * Verification receipt UUID for diagnosing blockers in a project verification graph.
     */
    run: string,
  }): CancelablePromise<ProjectBindingVerificationRunDiagnostics> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/{run}/diagnostics',
      path: {
        'slug': slug,
        'environment': environment,
        'run': run,
      },
      errors: {
        400: `Invalid receipt UUID.`,
        403: `Read access to every reviewed binding domain is required.`,
        404: `No owned receipt was found.`,
        503: `Receipt metadata could not be read.`,
      },
    });
  }
  /**
   * Review verification without creating receipts or admitting work
   * @returns ProjectBindingVerificationReview Server plan, blockers, required consent and opaque review digests. Ready means the plan is available; it is not verification evidence.
   * @throws ApiError
   */
  public static reviewProjectBindingVerification({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for reviewing project graph verification before receipt creation.
     */
    slug: string,
    /**
     * Project environment for reviewing project graph verification before receipt creation.
     */
    environment: string,
    requestBody: ReviewProjectBindingVerificationRequest,
  }): CancelablePromise<ProjectBindingVerificationReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/review',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `Project verification review. Read access to every reviewed binding domain is required.`,
        409: `The selected deployment, policy or prepared manifest changed.`,
        503: `A complete review could not be read.`,
      },
    });
  }
  /**
   * completeProjectBindingVerificationRun
   * @returns ProjectBindingVerificationRun Frozen graph verification run; qualification does not change traffic.
   * @throws ApiError
   */
  public static completeProjectBindingVerificationRun({
    slug,
    environment,
    run,
  }: {
    /**
     * Slug of the project for finalizing a retained project verification graph.
     */
    slug: string,
    /**
     * Project environment for finalizing a retained project verification graph.
     */
    environment: string,
    /**
     * Verification receipt UUID for finalizing a retained project verification graph.
     */
    run: string,
  }): CancelablePromise<ProjectBindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/{run}/complete',
      path: {
        'slug': slug,
        'environment': environment,
        'run': run,
      },
      errors: {
        409: `Frozen membership, routing, policy, configuration or evidence changed.`,
        429: `Normal admission or receipt capacity reached.`,
      },
    });
  }
  /**
   * resumeProjectBindingVerificationRun
   * @returns ProjectBindingVerificationRun Project graph reconciliation. Frozen graph verification run; qualification does not change traffic.
   * @throws ApiError
   */
  public static resumeProjectBindingVerificationRun({
    slug,
    environment,
    run,
  }: {
    /**
     * Slug of the project for reconciling unfinished project verification work.
     */
    slug: string,
    /**
     * Project environment for reconciling unfinished project verification work.
     */
    environment: string,
    /**
     * Verification receipt UUID for reconciling unfinished project verification work.
     */
    run: string,
  }): CancelablePromise<ProjectBindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/{run}/resume',
      path: {
        'slug': slug,
        'environment': environment,
        'run': run,
      },
      errors: {
        409: `Project graph reconciliation. Frozen membership, routing, policy, configuration or evidence changed.`,
        429: `Project graph reconciliation. Normal admission or receipt capacity reached.`,
      },
    });
  }
  /**
   * getProjectBindingVerificationRun
   * @returns ProjectBindingVerificationRun Project graph status. Frozen graph verification run; qualification does not change traffic.
   * @throws ApiError
   */
  public static getProjectBindingVerificationRun({
    slug,
    environment,
    run,
  }: {
    /**
     * Slug of the project for reading retained project graph verification status.
     */
    slug: string,
    /**
     * Project environment for reading retained project graph verification status.
     */
    environment: string,
    /**
     * Verification receipt UUID for reading retained project graph verification status.
     */
    run: string,
  }): CancelablePromise<ProjectBindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/{run}',
      path: {
        'slug': slug,
        'environment': environment,
        'run': run,
      },
      errors: {
        409: `Project graph status. Frozen membership, routing, policy, configuration or evidence changed.`,
        429: `Project graph status. Normal admission or receipt capacity reached.`,
      },
    });
  }
  /**
   * cancelProjectBindingVerificationRun
   * @returns ProjectBindingVerificationRun Project graph cancellation. Frozen graph verification run; qualification does not change traffic.
   * @throws ApiError
   */
  public static cancelProjectBindingVerificationRun({
    slug,
    environment,
    run,
  }: {
    /**
     * Slug of the project for requesting cancellation of a project verification graph.
     */
    slug: string,
    /**
     * Project environment for requesting cancellation of a project verification graph.
     */
    environment: string,
    /**
     * Verification receipt UUID for requesting cancellation of a project verification graph.
     */
    run: string,
  }): CancelablePromise<ProjectBindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/{run}',
      path: {
        'slug': slug,
        'environment': environment,
        'run': run,
      },
      errors: {
        409: `Project graph cancellation. Frozen membership, routing, policy, configuration or evidence changed.`,
        429: `Project graph cancellation. Normal admission or receipt capacity reached.`,
      },
    });
  }
  /**
   * List retained verification receipt summaries without requalifying evidence.
   * Newest first with creation-time and UUID keyset pagination. Reuse filters with next_cursor. observed_at freezes the expiry cutoff; recorded state may change between pages. Listings grant no admission or release authority.
   * @returns BindingVerificationRunList Bounded retained receipt summaries; not a permanent history or proof of current qualification.
   * @throws ApiError
   */
  public static listProjectBindingVerificationRuns({
    slug,
    environment,
    limit = 20,
    cursor,
    deploymentId,
    status,
  }: {
    /**
     * Slug of the project for listing retained project graph verification receipts.
     */
    slug: string,
    /**
     * Project environment for listing retained project graph verification receipts.
     */
    environment: string,
    /**
     * Maximum retained project verification receipts returned on this page.
     */
    limit?: number,
    /**
     * Opaque continuation cursor from the previous project verification receipt page.
     */
    cursor?: string,
    /**
     * Restrict retained project verification receipts to this exact deployment UUID.
     */
    deploymentId?: string,
    /**
     * Restrict project verification receipt discovery to this effective retained status.
     */
    status?: 'pending' | 'passed' | 'cancelled' | 'expired',
  }): CancelablePromise<BindingVerificationRunList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
        'deployment_id': deploymentId,
        'status': status,
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
      },
    });
  }
  /**
   * createProjectBindingVerificationRun
   * @returns ProjectBindingVerificationRun Project graph creation. Frozen graph verification run; qualification does not change traffic.
   * @throws ApiError
   */
  public static createProjectBindingVerificationRun({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for creating a reviewed project verification graph.
     */
    slug: string,
    /**
     * Project environment for creating a reviewed project verification graph.
     */
    environment: string,
    requestBody: CreateProjectBindingVerificationRunRequest,
  }): CancelablePromise<ProjectBindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        409: `Project graph creation. Frozen membership, routing, policy, configuration or evidence changed.`,
        429: `Project graph creation. Normal admission or receipt capacity reached.`,
      },
    });
  }
  /**
   * Deactivate a checked project release set after verifying fallback recipients
   * Requires one eligible materialized live 100% fallback per current workload,
   * a current active graph and enforced binding policies on every fallback.
   * Preparation observes routes without changing them. Use the returned manifest
   * to qualify exact fallback service dependencies, then deactivate explicitly.
   * ttl_seconds is zero because the retired graph keeps its existing TTL.
   * Deactivation compares active graph, complete fallback routing, current policies,
   * fresh evidence, artifacts and service readiness atomically. PostgreSQL rechecks
   * routing and evidence expiry at commit. Missing routes and traffic splits block.
   * The retired release contains an immutable deactivation receipt. Use an
   * Idempotency-Key for retries. GitOps-managed workloads remain blocked.
   *
   * @returns CheckedProjectReleaseResponse Deactivate the current graph after checking every fallback recipient.
   * @throws ApiError
   */
  public static deactivateCheckedProjectRelease({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for deactivating a checked project release set.
     */
    slug: string,
    /**
     * Project environment for deactivating a checked project release set.
     */
    environment: string,
    requestBody: CheckedProjectReleaseManifest,
  }): CancelablePromise<CheckedProjectReleaseResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/deactivate',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Prepare checked release-set deactivation to exact weighted fallback routes
   * Checked release-set deactivation preparation. Requires one eligible materialized live 100% fallback per current workload,
   * a current active graph and enforced binding policies on every fallback.
   * Preparation observes routes without changing them. Use the returned manifest
   * to qualify exact fallback service dependencies, then deactivate explicitly.
   * ttl_seconds is zero because the retired graph keeps its existing TTL.
   * Deactivation compares active graph, complete fallback routing, current policies,
   * fresh evidence, artifacts and service readiness atomically. PostgreSQL rechecks
   * routing and evidence expiry at commit. Missing routes and traffic splits block.
   * The retired release contains an immutable deactivation receipt. Use an
   * Idempotency-Key for retries. GitOps-managed workloads remain blocked.
   *
   * @returns CheckedProjectReleaseManifest Prepare checked return to exact 100% weighted fallback routes.
   * @throws ApiError
   */
  public static prepareCheckedProjectDeactivation({
    slug,
    environment,
  }: {
    /**
     * Slug of the project for reviewing exact weighted fallbacks before graph deactivation.
     */
    slug: string,
    /**
     * Project environment for reviewing exact weighted fallbacks before graph deactivation.
     */
    environment: string,
  }): CancelablePromise<CheckedProjectReleaseManifest> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/prepare-deactivation',
      path: {
        'slug': slug,
        'environment': environment,
      },
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
      },
    });
  }
  /**
   * Restore a historical project release set using fresh binding checks
   * Selects an inactive, unexpired historical release in this project environment.
   * Requires a different expected active release, exact historical membership,
   * current enforced policies, fresh target-specific evidence and ready artifacts
   * and service replicas. Historical expiry is checked again at commit.
   * Publishes a new immutable release recording rollback_source_release_id and
   * rollback_previous_release_id; history never grants a policy waiver.
   * Use an Idempotency-Key for mutation retries. No deployments are awakened.
   *
   * @returns CheckedProjectReleaseResponse Recover a retained historical graph with fresh binding evidence.
   * @throws ApiError
   */
  public static rollbackCheckedProjectRelease({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for restoring a retained graph with fresh binding qualification.
     */
    slug: string,
    /**
     * Project environment for restoring a retained graph with fresh binding qualification.
     */
    environment: string,
    requestBody: CheckedProjectReleaseManifest,
  }): CancelablePromise<CheckedProjectReleaseResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/rollback',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Prepare checked release-set recovery to a selected retained graph
   * Checked release-set rollback preparation. Selects an inactive, unexpired historical release in this project environment.
   * Requires a different expected active release, exact historical membership,
   * current enforced policies, fresh target-specific evidence and ready artifacts
   * and service replicas. Historical expiry is checked again at commit.
   * Publishes a new immutable release recording rollback_source_release_id and
   * rollback_previous_release_id; history never grants a policy waiver.
   * Use an Idempotency-Key for mutation retries. No deployments are awakened.
   *
   * @returns CheckedProjectReleaseManifest Prepare recovery to an explicit retained historical graph.
   * @throws ApiError
   */
  public static prepareCheckedProjectRollback({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for reviewing recovery to a selected historical release graph.
     */
    slug: string,
    /**
     * Project environment for reviewing recovery to a selected historical release graph.
     */
    environment: string,
    requestBody: PrepareProjectRollbackRequest,
  }): CancelablePromise<CheckedProjectReleaseManifest> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/prepare-rollback',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Activate a checked project release set with pinned binding evidence
   * Requires an enforced binding release policy on every project member scope.
   * Preparation freezes exact deployments, active graph or weighted fallback,
   * policy revisions and internal service targets. It is a metadata observation,
   * not an evidence grant. Qualification uses candidate-specific task probes.
   * Activation independently checks all member evidence, service edges, artifacts,
   * full project membership, current policy, evidence expiry, active graph CAS
   * and service replica readiness in one transaction. A blocked graph keeps
   * current routes. No target handler calls or wakes are made by service probes.
   * Generic graph publication and deactivation remain fail closed under enforcement.
   *
   * @returns CheckedProjectReleaseResponse Activate a pinned graph with fresh checked binding evidence.
   * @throws ApiError
   */
  public static activateCheckedProjectRelease({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for activating a pinned project graph after binding checks.
     */
    slug: string,
    /**
     * Project environment for activating a pinned project graph after binding checks.
     */
    environment: string,
    requestBody: CheckedProjectReleaseManifest,
  }): CancelablePromise<CheckedProjectReleaseResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/activate',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Prepare a project release-set manifest for checked binding qualification
   * Checked release-set preparation. Requires an enforced binding release policy on every project member scope.
   * Preparation freezes exact deployments, active graph or weighted fallback,
   * policy revisions and internal service targets. It is a metadata observation,
   * not an evidence grant. Qualification uses candidate-specific task probes.
   * Activation independently checks all member evidence, service edges, artifacts,
   * full project membership, current policy, evidence expiry, active graph CAS
   * and service replica readiness in one transaction. A blocked graph keeps
   * current routes. No target handler calls or wakes are made by service probes.
   * Generic graph publication and deactivation remain fail closed under enforcement.
   *
   * @returns CheckedProjectReleaseManifest Prepare a pinned graph manifest without changing routes.
   * @throws ApiError
   */
  public static prepareCheckedProjectRelease({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for preparing binding qualification for a pinned release-set manifest.
     */
    slug: string,
    /**
     * Project environment for preparing binding qualification for a pinned release-set manifest.
     */
    environment: string,
    requestBody: PublishProjectReleaseSetRequest,
  }): CancelablePromise<CheckedProjectReleaseManifest> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/prepare',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Observe a candidate staging lease.
   * @returns ProjectReleaseStage Current staging lease and observed service readiness; this conveys no binding evidence.
   * @throws ApiError
   */
  public static getProjectReleaseStage({
    slug,
    environment,
    stage,
  }: {
    /**
     * Slug of the project for observing or cancelling a candidate service staging lease.
     */
    slug: string,
    /**
     * Project environment for observing or cancelling a candidate service staging lease.
     */
    environment: string,
    /**
     * Identifier of the candidate service staging lease selected for status or cancellation.
     */
    stage: string,
  }): CancelablePromise<ProjectReleaseStage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/staging/{stage}',
      path: {
        'slug': slug,
        'environment': environment,
        'stage': stage,
      },
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
      },
    });
  }
  /**
   * Cancel temporary candidate capacity; active graph and traffic allocations remain authoritative.
   * @returns ProjectReleaseStage Candidate service staging cancellation. Current staging lease and observed service readiness; this conveys no binding evidence.
   * @throws ApiError
   */
  public static cancelProjectReleaseStage({
    slug,
    environment,
    stage,
  }: {
    /**
     * Slug of the project for observing or cancelling a candidate service staging lease.
     */
    slug: string,
    /**
     * Project environment for observing or cancelling a candidate service staging lease.
     */
    environment: string,
    /**
     * Identifier of the candidate service staging lease selected for status or cancellation.
     */
    stage: string,
  }): CancelablePromise<ProjectReleaseStage> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/staging/{stage}',
      path: {
        'slug': slug,
        'environment': environment,
        'stage': stage,
      },
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
      },
    });
  }
  /**
   * Lease candidate service capacity without publishing routes.
   * Stages the exact service deployments in a complete prepared forward or rollback manifest.
   * Non-GitOps live materialized candidates only; mirror recipients and active service rollouts are excluded.
   * Policy and routing changes fail closed.
   * Schedd uses normal admission, preserves serving allocations and never grants extra concurrency.
   * Capacity is billable. Repeating the same manifest reuses its unexpired lease without extending it.
   * A different manifest conflicts until the current stage is cancelled or expires.
   * Qualification and activation remain explicit and independently recheck readiness and evidence.
   *
   * @returns ProjectReleaseStage Candidate service staging admission. Current staging lease and observed service readiness; this conveys no binding evidence.
   * @throws ApiError
   */
  public static stageCheckedProjectRelease({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Slug of the project for temporarily admitting selected candidate services for staging.
     */
    slug: string,
    /**
     * Project environment for temporarily admitting selected candidate services for staging.
     */
    environment: string,
    requestBody: StageProjectReleaseRequest,
  }): CancelablePromise<ProjectReleaseStage> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/release-sets/staging',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * List projects owned by a company organization.
   * @returns ProjectSummaryResponse Projects visible to the current company member.
   * @throws ApiError
   */
  public static listOrgProjects({
    organization,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
  }): CancelablePromise<Array<ProjectSummaryResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects',
      path: {
        'organization': organization,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create or apply an organization project from uploaded source.
   * Upload source and project_slug using the existing multipart contract. An adopted organization supplies the new project resource namespace and deployment budget. Database and async-route manifest bindings are rejected before personal services are resolved.
   * @returns ApplyResponse Applied organization project and workload results.
   * @throws ApiError
   */
  public static applyOrgProject({
    organization,
    formData,
    planToken,
    idempotencyKey,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    formData: ProjectApplyRequest,
    /**
     * Plan token returned by the organization source scan; must match the uploaded source and addressed project.
     */
    planToken?: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects',
      path: {
        'organization': organization,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'plan_token': planToken,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect an organization project and its workloads.
   * @returns ProjectResponse Company project metadata, exclusions and recovery state.
   * @throws ApiError
   */
  public static getOrgProject({
    organization,
    slug,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
  }): CancelablePromise<ProjectResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}',
      path: {
        'organization': organization,
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Update an organization project production branch.
   * @returns ProjectResponse Updated company project source settings.
   * @throws ApiError
   */
  public static updateOrgProject({
    organization,
    slug,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    requestBody: UpdateProjectRequest,
  }): CancelablePromise<ProjectResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/orgs/{organization}/projects/{slug}',
      path: {
        'organization': organization,
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete a company project and detach its workloads.
   * Owners and admins may remove the company project. Its workloads remain live and are detached from the deleted project.
   * @returns void
   * @throws ApiError
   */
  public static deleteOrgProject({
    organization,
    slug,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/projects/{slug}',
      path: {
        'organization': organization,
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List an organization project environment registry.
   * @returns ProjectEnvironmentResponse Registered environments for this company project.
   * @throws ApiError
   */
  public static listOrgProjectEnvironments({
    organization,
    slug,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
  }): CancelablePromise<Array<ProjectEnvironmentResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments',
      path: {
        'organization': organization,
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create an empty organization project environment.
   * Developers may create an empty environment. Creating it protected requires an owner or admin. from_environment and full cloning return a conflict until shared resource cloning is supported.
   * @returns ProjectEnvironmentResponse New empty company environment and protection policy.
   * @throws ApiError
   */
  public static createOrgProjectEnvironment({
    organization,
    slug,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    requestBody: CreateProjectEnvironmentRequest,
  }): CancelablePromise<ProjectEnvironmentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments',
      path: {
        'organization': organization,
        'slug': slug,
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
      },
    });
  }
  /**
   * Inspect a registered company project environment.
   * @returns ProjectEnvironmentResponse Company environment metadata and current release state.
   * @throws ApiError
   */
  public static getOrgProjectEnvironment({
    organization,
    slug,
    environment,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Change protection on an organization project environment.
   * @returns ProjectEnvironmentResponse Updated company environment protection settings.
   * @throws ApiError
   */
  public static updateOrgProjectEnvironment({
    organization,
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    requestBody: UpdateProjectEnvironmentRequest,
  }): CancelablePromise<ProjectEnvironmentResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Delete an unused company project environment.
   * An owner or admin may delete an unprotected, unused, non-production company environment. Existing release, configuration and resource guards still apply.
   * @returns any Unused company project environment deleted.
   * @throws ApiError
   */
  public static deleteOrgProjectEnvironment({
    organization,
    slug,
    environment,
    idempotencyKey,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read live workload releases in a company environment.
   * Each company workload contributes non-secret live release metadata. Stable environment URLs become available when a live deployment exists.
   * @returns ProjectEnvironmentReleaseListResponse Non-secret live release metadata for the company environment.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentReleases({
    organization,
    slug,
    environment,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentReleaseListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/releases',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List organization project release-set history.
   * Company release graphs are ordered newest first. Supply next_before as before to continue through retained history.
   * @returns ProjectReleaseSetListResponse Newest-first release graphs retained by this company project.
   * @throws ApiError
   */
  public static listOrgProjectReleaseSets({
    organization,
    slug,
    environment,
    before,
    limit = 50,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Opaque next_before cursor from this company project history.
     */
    before?: string,
    /**
     * Maximum company history entries returned in one page; capped at 100.
     */
    limit?: number,
  }): CancelablePromise<ProjectReleaseSetListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      query: {
        'before': before,
        'limit': limit,
      },
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
      },
    });
  }
  /**
   * Activate an immutable organization project release graph.
   * Every company workload must have a live deployment with revision pinning that covers the requested TTL. Earlier organization release sets stay addressable until expiry.
   * @returns ProjectReleaseSetResponse Newly activated company project deployment graph.
   * @throws ApiError
   */
  public static publishOrgProjectReleaseSet({
    organization,
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    requestBody: PublishProjectReleaseSetRequest,
  }): CancelablePromise<ProjectReleaseSetResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Read the active organization project release graph.
   * The addressed company environment must have an active release graph; otherwise this operation returns 404.
   * @returns ProjectReleaseSetResponse Active release graph in the requested company environment.
   * @throws ApiError
   */
  public static getActiveOrgProjectReleaseSet({
    organization,
    slug,
    environment,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL. The value is resolved in the selected project.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project. The value is resolved in the selected project.
     */
    environment: string,
  }): CancelablePromise<ProjectReleaseSetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets/active',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
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
      },
    });
  }
  /**
   * Inspect a retained organization project release set.
   * Retired and expired company graphs remain inspectable for diagnosis. Inspecting one does not reactivate its routing.
   * @returns ProjectReleaseSetResponse Retained company release graph, including retired or expired members.
   * @throws ApiError
   */
  public static getOrgProjectReleaseSet({
    organization,
    slug,
    environment,
    release,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL. The value is resolved in the selected project.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project. The value is resolved in the selected project.
     */
    environment: string,
    /**
     * Immutable release-set UUID retained by the company project.
     */
    release: string,
  }): CancelablePromise<ProjectReleaseSetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets/{release}',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'release': release,
      },
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
      },
    });
  }
  /**
   * Read effective state for a company project environment.
   * Returns the company environment configuration, live releases, runtime variables and secret metadata without revealing secret values.
   * @returns ProjectEnvironmentStateResponse Effective company configuration, routes and live release references.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentState({
    organization,
    slug,
    environment,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentStateResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/state',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Compare effective state between company project environments.
   * Compares effective company environment configuration, workload releases, variables, secret metadata and declared routes.
   * @returns ProjectEnvironmentDiffResponse Non-secret effective-state differences within the company project.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentDiff({
    organization,
    slug,
    environment,
    from,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Source environment filter within this company project.
     */
    from: string,
  }): CancelablePromise<ProjectEnvironmentDiffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/diff',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      query: {
        'from': from,
      },
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
      },
    });
  }
  /**
   * Read organization project environment configuration.
   * @returns ProjectEnvironmentConfigResponse Latest immutable non-secret configuration for the company environment.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentConfig({
    organization,
    slug,
    environment,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
  }): CancelablePromise<ProjectEnvironmentConfigResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/config',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Append an organization environment configuration version.
   * Publishes a new immutable company configuration snapshot. Secret-shaped keys are refused; use the reviewed application secret APIs for sensitive values.
   * @returns ProjectEnvironmentConfigResponse Published company environment configuration version and hash.
   * @throws ApiError
   */
  public static updateOrgProjectEnvironmentConfig({
    organization,
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    requestBody: UpdateProjectEnvironmentConfigRequest,
  }): CancelablePromise<ProjectEnvironmentConfigResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/config',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Compare company environment configuration snapshots.
   * @returns ProjectEnvironmentConfigDiffResponse Key-level differences between company configuration snapshots.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentConfigDiff({
    organization,
    slug,
    environment,
    from,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Source environment filter within this company project.
     */
    from: string,
  }): CancelablePromise<ProjectEnvironmentConfigDiffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/config/diff',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      query: {
        'from': from,
      },
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
      },
    });
  }
  /**
   * Preview promotion between organization project environments.
   * Builds a company promotion plan from current source and target state. Protected target environments require an owner or admin to approve the exact returned plan.
   * @returns ProjectEnvironmentPromotionPreviewResponse Current company promotion plan and its bound preview token.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentPromotionPreview({
    organization,
    slug,
    environment,
    from,
    syncConfig = false,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Source environment filter within this company project.
     */
    from: string,
    /**
     * Copy non-secret company source configuration together with the target promotion graph.
     */
    syncConfig?: boolean,
  }): CancelablePromise<ProjectEnvironmentPromotionPreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotion-preview',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      query: {
        'from': from,
        'sync_config': syncConfig,
      },
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
      },
    });
  }
  /**
   * Preview a company promotion with destination production secret changes.
   * Read-only, bounded preview with immutable secret version references, with the same rules as the personal secret preview. Requires MFA, secrets write permission and deployment permission in the organization.
   * @returns ProjectEnvironmentPromotionPreviewResponse Company promotion preview listing secret key changes as version references only.
   * @throws ApiError
   */
  public static previewOrgProjectEnvironmentPromotionSecrets({
    organization,
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    requestBody: ProjectEnvironmentPromotionSecretPreviewRequest,
  }): CancelablePromise<ProjectEnvironmentPromotionPreviewResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotion-preview',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Company secret releases or destination verification are unavailable.`,
      },
    });
  }
  /**
   * Inspect a protected company environment approval.
   * Reads the company approval lifecycle without returning its raw one-time approval token.
   * @returns ProjectEnvironmentApprovalStatusResponse Lifecycle metadata for a company approval, without its raw token.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentApprovalStatus({
    organization,
    slug,
    environment,
    approval,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Plan approval UUID belonging to this company project environment.
     */
    approval: string,
  }): CancelablePromise<ProjectEnvironmentApprovalStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/approvals/{approval}',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'approval': approval,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect an organization project promotion operation.
   * Includes the company promotion cutover state, post-cutover verification result and safe workload checkpoints.
   * @returns ProjectEnvironmentPromotionStatusResponse Company promotion status, verification and workload checkpoints.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentPromotionStatus({
    organization,
    slug,
    environment,
    promotion,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Durable promotion identifier in the company environment history.
     */
    promotion: string,
  }): CancelablePromise<ProjectEnvironmentPromotionStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List organization project environment promotions.
   * Returns compact company promotion history. Read a specific promotion operation for its complete workload checkpoints.
   * @returns ProjectEnvironmentPromotionListResponse Newest-first company promotion history with continuation cursor.
   * @throws ApiError
   */
  public static listOrgProjectEnvironmentPromotions({
    organization,
    slug,
    environment,
    before,
    limit = 50,
    from,
    status,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Opaque next_before cursor from this company project history. The value is resolved in the selected project.
     */
    before?: string,
    /**
     * Maximum company history entries returned in one page; capped at 100. The value is resolved in the selected project.
     */
    limit?: number,
    /**
     * Source environment filter within this company project. A slug pattern is validated.
     */
    from?: string,
    /**
     * Promotion lifecycle status to include from the company history.
     */
    status?: 'running' | 'succeeded' | 'failed',
  }): CancelablePromise<ProjectEnvironmentPromotionListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      query: {
        'before': before,
        'limit': limit,
        'from': from,
        'status': status,
      },
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
      },
    });
  }
  /**
   * Preview deletion of an organization project.
   * @returns ProjectDeletePreviewResponse Company workloads and related state affected by project deletion.
   * @throws ApiError
   */
  public static previewDeleteOrgProject({
    organization,
    slug,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
  }): CancelablePromise<ProjectDeletePreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/delete-preview',
      path: {
        'organization': organization,
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replace declared routes in a company project environment.
   * The declared contract applies to the company workload environment URLs. The ordinary app hostname keeps its application-wide contract.
   * @returns ProjectEnvironmentRoutePolicyResponse Updated company workload route contract and revision.
   * @throws ApiError
   */
  public static updateOrgProjectEnvironmentRoutes({
    organization,
    slug,
    environment,
    workload,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL. The value is resolved in the selected project.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project. The value is resolved in the selected project.
     */
    environment: string,
    /**
     * Workload name registered in the company project environment.
     */
    workload: string,
    requestBody: UpdateProjectEnvironmentRoutePolicyRequest,
  }): CancelablePromise<ProjectEnvironmentRoutePolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/routes',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'workload': workload,
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
      },
    });
  }
  /**
   * Remove a persisted company project deployment exclusion.
   * Removes a company workload exclusion saved by a previous persist-exclude deployment so a later source apply can include it again.
   * @returns any Company workload exclusion removed.
   * @throws ApiError
   */
  public static orgProjectDeleteDeploymentScopeExclusion({
    organization,
    slug,
    slug2,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL. The value is resolved in the selected project.
     */
    slug: string,
    /**
     * Company workload app slug whose saved deployment exclusion is removed.
     */
    slug2: string,
  }): CancelablePromise<{
    ok?: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/projects/{slug}/exclusions/{slug2}',
      path: {
        'organization': organization,
        'slug': slug,
        'slug2': slug2,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `The company project or saved workload exclusion was not found; both return scope_exclusion_not_found.`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Preview an organization source upload before applying it.
   * Accepts a source tarball and project_slug without mutating the organization project. Existing projects in a legacy creator namespace must use their project-specific scan URL.
   * @returns PlanResponse Company source scan, workload partitions and deployment plan.
   * @throws ApiError
   */
  public static scanOrgProject({
    organization,
    formData,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    formData: ProjectScanRequest,
  }): CancelablePromise<PlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/scan',
      path: {
        'organization': organization,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Record qualification of a company project release set.
   * Binds closed-schema health and smoke evidence to the exact active release set in this company environment.
   * @returns ProjectEnvironmentQualificationResponse Qualification evidence bound to the active company release graph.
   * @throws ApiError
   */
  public static createOrgProjectEnvironmentQualification({
    organization,
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    requestBody: CreateProjectEnvironmentQualificationRequest,
  }): CancelablePromise<ProjectEnvironmentQualificationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/qualifications',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Promote live workloads between company project environments.
   * Revalidates current company source and target state before cutover. A protected target consumes an approval bound to that exact plan. The authenticated teammate is recorded as the deployment actor.
   * @returns ProjectEnvironmentPromotionResponse Durable company promotion result and per-workload deployment references.
   * @throws ApiError
   */
  public static promoteOrgProjectEnvironment({
    organization,
    slug,
    environment,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Company operation replay key, partitioned by the authenticated actor and credential.
     */
    idempotencyKey: string,
    requestBody: PromoteProjectEnvironmentRequest,
  }): CancelablePromise<ProjectEnvironmentPromotionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promote',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
      },
    });
  }
  /**
   * Restore company workloads from a recorded promotion.
   * Restores the target deployments captured before this company promotion. Membership and deployment permission are checked before replaying an existing rollback receipt.
   * @returns ProjectEnvironmentPromotionStatusResponse Company rollback result restoring captured target deployments.
   * @throws ApiError
   */
  public static rollbackOrgProjectEnvironmentPromotion({
    organization,
    slug,
    environment,
    promotion,
    idempotencyKey,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Durable promotion identifier in the company environment history.
     */
    promotion: string,
    /**
     * Company operation replay key, partitioned by the authenticated actor and credential.
     */
    idempotencyKey: string,
  }): CancelablePromise<ProjectEnvironmentPromotionStatusResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/rollback',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
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
      },
    });
  }
  /**
   * Abort an in-progress company environment promotion.
   * Generation-fenced operator decision on a company promotion, with the same semantics as the personal abort route. Requires deployment permission in the organization.
   * @returns ProjectPromotionControl Terminal acknowledgement of the company abort decision.
   * @throws ApiError
   */
  public static abortOrgProjectEnvironmentPromotion({
    organization,
    slug,
    environment,
    promotion,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Durable promotion identifier in the company environment history.
     */
    promotion: string,
    requestBody: ProjectPromotionControlRequest,
  }): CancelablePromise<ProjectPromotionControl> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/abort',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Company abort refused: the generation, request identity, phase or destination intent changed; inspect the promotion.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Retry guarded restoration of a blocked company environment promotion.
   * Retries a blocked company restoration with fresh baseline health and worker startup proof, like the personal recover route. Requires deployment permission in the organization.
   * @returns ProjectPromotionControl Terminal acknowledgement of the company restoration retry.
   * @throws ApiError
   */
  public static recoverOrgProjectEnvironmentPromotion({
    organization,
    slug,
    environment,
    promotion,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Durable promotion identifier in the company environment history.
     */
    promotion: string,
    requestBody: ProjectPromotionControlRequest,
  }): CancelablePromise<ProjectPromotionControl> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/recover',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Company restoration retry refused: the inspected generation, request identity, phase or destination intent no longer allows it.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Recover an interrupted company promotion migration without replaying SQL.
   * Records explicit database inspection and resumes or resolves an interrupted company promotion migration, like the personal recover-migration route. Requires deployment permission in the organization.
   * @returns ProjectPromotionMigrationRecovery Existing terminal acknowledgement of the company migration recovery.
   * @throws ApiError
   */
  public static recoverOrgProjectEnvironmentPromotionMigration({
    organization,
    slug,
    environment,
    promotion,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    /**
     * Durable promotion identifier in the company environment history.
     */
    promotion: string,
    requestBody: ProjectPromotionMigrationRecoveryRequest,
  }): CancelablePromise<ProjectPromotionMigrationRecovery> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/recover-migration',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'promotion': promotion,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Company migration recovery refused: the task, generation, runtime or destination intent changed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replace company environment headers and CORS policy.
   * An owner or admin may replace environment-specific headers and CORS rules. An explicit empty list disables inherited rules on that company environment URL.
   * @returns ProjectEnvironmentEdgePolicyResponse Published company workload headers and CORS revision.
   * @throws ApiError
   */
  public static updateOrgProjectEnvironmentPolicies({
    organization,
    slug,
    environment,
    workload,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL. The value is resolved in the selected project.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project. The value is resolved in the selected project.
     */
    environment: string,
    /**
     * Workload name registered in the company project environment.
     */
    workload: string,
    requestBody: UpdateProjectEnvironmentEdgePolicyRequest,
  }): CancelablePromise<ProjectEnvironmentEdgePolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/policies',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'workload': workload,
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
      },
    });
  }
  /**
   * Approve an exact protected organization environment plan.
   * @returns ProjectEnvironmentApprovalResponse Company approval receipt and one-time plan-bound token.
   * @throws ApiError
   */
  public static approveOrgProjectEnvironment({
    organization,
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    /**
     * Registered environment in the addressed organization project.
     */
    environment: string,
    requestBody: CreateProjectEnvironmentApprovalRequest,
  }): CancelablePromise<ProjectEnvironmentApprovalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/approvals',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Preview source for an existing organization project.
   * The uploaded project_slug must match the addressed company project. This preview preserves existing resource IDs and works with legacy creator namespaces after erasure.
   * @returns PlanResponse Deployment preview bound to the addressed company project.
   * @throws ApiError
   */
  public static scanOrgProjectDeployment({
    organization,
    slug,
    formData,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    formData: ProjectScanRequest,
  }): CancelablePromise<PlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/scan',
      path: {
        'organization': organization,
        'slug': slug,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Deploy uploaded source to an existing company project.
   * Uses the existing company project resource namespace even after creator erasure. The uploaded slug must match the URL; protected environments require an exact approved plan.
   * @returns ApplyResponse Applied source results in the existing company resource namespace.
   * @throws ApiError
   */
  public static deployOrgProject({
    organization,
    slug,
    formData,
    planToken,
    idempotencyKey,
  }: {
    /**
     * Company organization slug that owns the addressed project; requires current membership and compatible organization credentials.
     */
    organization: string,
    /**
     * Project slug within the organization named in the URL.
     */
    slug: string,
    formData: ProjectApplyRequest,
    /**
     * Plan token returned by the organization source scan; must match the uploaded source and addressed project.
     */
    planToken?: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ApplyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/projects/{slug}/deploy',
      path: {
        'organization': organization,
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'plan_token': planToken,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Organization queues: Read a stage workload's complete desired queue configuration
   * Organization queues: Read a stage workload's complete desired queue configuration. Owners, admins, developers and viewers can read metadata. Requires current membership and the corresponding credential scope. Mutations require organization billing adoption. IDs, messages, retries and receipts survive creator erasure. Returns logical definitions and the current workload revision. A missing
   * collection returns 409 environment_queue_collection_unavailable and never
   * inherits production queues. X-Gregale-Workload-Revision identifies the
   * head to use when initializing or replacing the collection.
   * Consumer activation is currently unavailable; saved definitions do not
   * activate delivery or qualify the stage for promotion.
   * @returns ProjectEnvironmentQueueBindingsResponse Organization queues: Read a stage workload's complete desired queue configuration. Complete stage-owned desired queue collection.
   * @throws ApiError
   */
  public static getOrgProjectEnvironmentQueueBindings({
    organization,
    slug,
    environment,
    workload,
  }: {
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Company owning the application or project.
     */
    organization: string,
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Project containing the workload and stage.
     */
    slug: string,
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Registered stage. Production is managed through the app queue-bindings API.
     */
    environment: string,
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Workload application slug belonging to this project.
     */
    workload: string,
  }): CancelablePromise<ProjectEnvironmentQueueBindingsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'workload': workload,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Organization queues: Replace a stage workload's complete desired queue configuration
   * Organization queues: Replace a stage workload's complete desired queue configuration. Owners, admins and developers can configure and operate queues. Requires current membership and the corresponding credential scope. Mutations require organization billing adoption. IDs, messages, retries and receipts survive creator erasure. Requires expected_revision to match the complete workload settings head,
   * with zero for an uninitialized workload. An explicit empty bindings list
   * removes all stage definitions. Stale revisions and protected stages
   * return 409, including protected no-op writes. Definitions have logical
   * names and no production consumer IDs. Existing deployments retain their
   * pinned definitions. Consumer activation is currently unavailable.
   * @returns ProjectEnvironmentQueueBindingsResponse Organization queues: Replace a stage workload's complete desired queue configuration. Stored desired queue definitions and their workload revision.
   * @throws ApiError
   */
  public static replaceOrgProjectEnvironmentQueueBindings({
    organization,
    slug,
    environment,
    workload,
    requestBody,
  }: {
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Company owning the application or project.
     */
    organization: string,
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Project containing the workload and stage.
     */
    slug: string,
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Registered stage. Production is managed through the app queue-bindings API.
     */
    environment: string,
    /**
     * Organization queues: Read a stage workload's complete desired queue configuration. Workload application slug belonging to this project.
     */
    workload: string,
    requestBody: ReplaceProjectEnvironmentQueueBindingsRequest,
  }): CancelablePromise<ProjectEnvironmentQueueBindingsResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings',
      path: {
        'organization': organization,
        'slug': slug,
        'environment': environment,
        'workload': workload,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
