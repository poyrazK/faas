/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyResponse } from '../models/ApplyResponse.js';
import type { CreateProjectEnvironmentApprovalRequest } from '../models/CreateProjectEnvironmentApprovalRequest.js';
import type { CreateProjectEnvironmentRequest } from '../models/CreateProjectEnvironmentRequest.js';
import type { PlanResponse } from '../models/PlanResponse.js';
import type { ProjectApplyRequest } from '../models/ProjectApplyRequest.js';
import type { ProjectDeletePreviewResponse } from '../models/ProjectDeletePreviewResponse.js';
import type { ProjectEnvironmentApprovalResponse } from '../models/ProjectEnvironmentApprovalResponse.js';
import type { ProjectEnvironmentConfigDiffResponse } from '../models/ProjectEnvironmentConfigDiffResponse.js';
import type { ProjectEnvironmentConfigResponse } from '../models/ProjectEnvironmentConfigResponse.js';
import type { ProjectEnvironmentPromotionPreviewResponse } from '../models/ProjectEnvironmentPromotionPreviewResponse.js';
import type { ProjectEnvironmentPromotionResponse } from '../models/ProjectEnvironmentPromotionResponse.js';
import type { ProjectEnvironmentResponse } from '../models/ProjectEnvironmentResponse.js';
import type { ProjectResponse } from '../models/ProjectResponse.js';
import type { ProjectScanRequest } from '../models/ProjectScanRequest.js';
import type { ProjectSourceRefScanRequest } from '../models/ProjectSourceRefScanRequest.js';
import type { ProjectSummaryResponse } from '../models/ProjectSummaryResponse.js';
import type { PromoteProjectEnvironmentRequest } from '../models/PromoteProjectEnvironmentRequest.js';
import type { UpdateProjectEnvironmentConfigRequest } from '../models/UpdateProjectEnvironmentConfigRequest.js';
import type { UpdateProjectEnvironmentRequest } from '../models/UpdateProjectEnvironmentRequest.js';
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
  }: {
    formData: ProjectScanRequest,
  }): CancelablePromise<PlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/scan',
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        413: `code: source_too_large`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
    planToken,
    idempotencyKey,
  }: {
    formData: ProjectApplyRequest,
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create a durable project environment.
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
     * Target environment receiving the configuration comparison.
     */
    environment: string,
    /**
     * Source environment to compare against the target.
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * mutations.
   *
   * @returns ProjectEnvironmentPromotionPreviewResponse Promotion preview and immutable promotion identity.
   * @throws ApiError
   */
  public static getProjectEnvironmentPromotionPreview({
    slug,
    environment,
    from,
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
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Execute a guarded promotion between project environments.
   * Revalidates the supplied promotion token against current live
   * deployments and environment configuration before promoting immutable
   * source artifacts. Protected targets also require an approval token
   * issued for that exact promotion. Target configuration and secrets are
   * never copied from the source environment.
   *
   * @returns ProjectEnvironmentPromotionResponse Promotion result for each project workload.
   * @throws ApiError
   */
  public static promoteProjectEnvironment({
    slug,
    environment,
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
    requestBody: PromoteProjectEnvironmentRequest,
  }): CancelablePromise<ProjectEnvironmentPromotionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/promote',
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
