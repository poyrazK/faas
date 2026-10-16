/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AdoptEnvironmentGitOpsRequest } from '../models/AdoptEnvironmentGitOpsRequest.js';
import type { AdoptEnvironmentGitOpsResponse } from '../models/AdoptEnvironmentGitOpsResponse.js';
import type { ApplyResponse } from '../models/ApplyResponse.js';
import type { ApproveEnvironmentGitRevisionRequest } from '../models/ApproveEnvironmentGitRevisionRequest.js';
import type { ApproveEnvironmentGitRevisionResponse } from '../models/ApproveEnvironmentGitRevisionResponse.js';
import type { CreateEnvironmentGitSourceRequest } from '../models/CreateEnvironmentGitSourceRequest.js';
import type { CreateProjectEnvironmentApprovalRequest } from '../models/CreateProjectEnvironmentApprovalRequest.js';
import type { CreateProjectEnvironmentQualificationRequest } from '../models/CreateProjectEnvironmentQualificationRequest.js';
import type { CreateProjectEnvironmentRequest } from '../models/CreateProjectEnvironmentRequest.js';
import type { DetachEnvironmentGitSourceRequest } from '../models/DetachEnvironmentGitSourceRequest.js';
import type { EnvironmentFieldOwnershipRequest } from '../models/EnvironmentFieldOwnershipRequest.js';
import type { EnvironmentFieldOwnershipResponse } from '../models/EnvironmentFieldOwnershipResponse.js';
import type { EnvironmentGitOpsOverrideRequest } from '../models/EnvironmentGitOpsOverrideRequest.js';
import type { EnvironmentGitOpsPlan } from '../models/EnvironmentGitOpsPlan.js';
import type { EnvironmentGitOpsStatusResponse } from '../models/EnvironmentGitOpsStatusResponse.js';
import type { EnvironmentGitSource } from '../models/EnvironmentGitSource.js';
import type { EnvironmentGitSourceUpdate } from '../models/EnvironmentGitSourceUpdate.js';
import type { PlanResponse } from '../models/PlanResponse.js';
import type { PreviewEnvironmentGitRevisionRequest } from '../models/PreviewEnvironmentGitRevisionRequest.js';
import type { PreviewEnvironmentGitRevisionResponse } from '../models/PreviewEnvironmentGitRevisionResponse.js';
import type { ProjectApplyRequest } from '../models/ProjectApplyRequest.js';
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
import type { ProjectEnvironmentStateResponse } from '../models/ProjectEnvironmentStateResponse.js';
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
   * account, fetches the selected ref through githubd, and runs the same
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
     * Require planned HTTP source service additions; disabled until native qualification. No removals or configuration changes.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
    slug: string,
    environment: string,
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
   * A durable generation-fenced operator decision. Before cutover, abort preserves destination intent and retires unused candidates. After cutover, abort or recover requires fresh server-owned baseline health and worker startup proof before atomic restoration. Recovery never rebuilds or extends retention. Migration incidents use recover-migration. Identical request replay returns that decision's durable receipt even after a newer decision.
   * @returns ProjectPromotionControl Terminal operator decision acknowledgement.
   * @throws ApiError
   */
  public static recoverProjectEnvironmentPromotion({
    slug,
    environment,
    promotion,
    requestBody,
  }: {
    slug: string,
    environment: string,
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
    slug: string,
    environment: string,
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
   * Operator escape hatch (ADR-124 code-review fix #2) for
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
}
