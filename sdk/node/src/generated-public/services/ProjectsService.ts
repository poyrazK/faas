/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyCachedProjectPlanRequest } from '../models/ApplyCachedProjectPlanRequest.js';
import type { ApplyResponse } from '../models/ApplyResponse.js';
import type { PlanResponse } from '../models/PlanResponse.js';
import type { ProjectApplyRequest } from '../models/ProjectApplyRequest.js';
import type { ProjectDeletePreviewResponse } from '../models/ProjectDeletePreviewResponse.js';
import type { ProjectDeploymentCheckRequest } from '../models/ProjectDeploymentCheckRequest.js';
import type { ProjectDeploymentReadiness } from '../models/ProjectDeploymentReadiness.js';
import type { ProjectMigrationRecoveryRequest } from '../models/ProjectMigrationRecoveryRequest.js';
import type { ProjectReleaseControlRequest } from '../models/ProjectReleaseControlRequest.js';
import type { ProjectReleaseSecretStageRequest } from '../models/ProjectReleaseSecretStageRequest.js';
import type { ProjectReleaseSecretStageResponse } from '../models/ProjectReleaseSecretStageResponse.js';
import type { ProjectResponse } from '../models/ProjectResponse.js';
import type { ProjectScanRequest } from '../models/ProjectScanRequest.js';
import type { ProjectSourceRefScanRequest } from '../models/ProjectSourceRefScanRequest.js';
import type { ProjectSubmissionReceipt } from '../models/ProjectSubmissionReceipt.js';
import type { ProjectSummaryResponse } from '../models/ProjectSummaryResponse.js';
import type { UpdateProjectRequest } from '../models/UpdateProjectRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class ProjectsService {
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
}
