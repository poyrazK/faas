/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AdvanceCanaryRequest } from '../models/AdvanceCanaryRequest.js';
import type { BindingPromotionRequest } from '../models/BindingPromotionRequest.js';
import type { BindingPromotionResponse } from '../models/BindingPromotionResponse.js';
import type { BuildListResponse } from '../models/BuildListResponse.js';
import type { BuildProvenanceResponse } from '../models/BuildProvenanceResponse.js';
import type { BuildResponse } from '../models/BuildResponse.js';
import type { CanaryAdvanceResponse } from '../models/CanaryAdvanceResponse.js';
import type { CancelDeploymentRequest } from '../models/CancelDeploymentRequest.js';
import type { ClearObsoleteReport } from '../models/ClearObsoleteReport.js';
import type { CreateDeploymentRequest } from '../models/CreateDeploymentRequest.js';
import type { DeploymentAliasListResponse } from '../models/DeploymentAliasListResponse.js';
import type { DeploymentAliasResponse } from '../models/DeploymentAliasResponse.js';
import type { DeploymentListResponse } from '../models/DeploymentListResponse.js';
import type { DeploymentPreviewURL } from '../models/DeploymentPreviewURL.js';
import type { DeploymentResponse } from '../models/DeploymentResponse.js';
import type { DeploymentRuntimeResponse } from '../models/DeploymentRuntimeResponse.js';
import type { DeploymentSummaryResponse } from '../models/DeploymentSummaryResponse.js';
import type { LatestDeploymentsByAppResponse } from '../models/LatestDeploymentsByAppResponse.js';
import type { ListDeploymentAuditResponse } from '../models/ListDeploymentAuditResponse.js';
import type { RecoverRolloutRequest } from '../models/RecoverRolloutRequest.js';
import type { RetryDeploymentRequest } from '../models/RetryDeploymentRequest.js';
import type { RollbackOperation } from '../models/RollbackOperation.js';
import type { RollbackRequest } from '../models/RollbackRequest.js';
import type { RolloutTransitionResponse } from '../models/RolloutTransitionResponse.js';
import type { RuntimeUpgradePreviewResponse } from '../models/RuntimeUpgradePreviewResponse.js';
import type { ScanResult } from '../models/ScanResult.js';
import type { SecretScanResult } from '../models/SecretScanResult.js';
import type { SetDeploymentAliasRequest } from '../models/SetDeploymentAliasRequest.js';
import type { SourceRefDeployRequest } from '../models/SourceRefDeployRequest.js';
import type { SourceTarballDeployRequest } from '../models/SourceTarballDeployRequest.js';
import type { UpdateDeploymentTrafficRequest } from '../models/UpdateDeploymentTrafficRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DeploymentsService {
  /**
   * List named deployment aliases for an app.
   * Returns customer-managed names that point to exact immutable deployment rows, with their stable routing hosts. Changing an alias does not alter production traffic.
   * @returns DeploymentAliasListResponse Alias names and their pinned deployment revisions.
   * @throws ApiError
   */
  public static listDeploymentAliases({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<DeploymentAliasListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployment-aliases',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Point a named alias at an immutable deployment.
   * The target must be a routable deployment that belongs to the app. This updates only the alias mapping; it does not shift production traffic. The alias name and app identifier must fit together in one DNS label.
   * @returns DeploymentAliasResponse Alias mapping after the update.
   * @throws ApiError
   */
  public static setDeploymentAlias({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Lowercase DNS label for this app's stable revision alias.
     */
    name: string,
    requestBody: SetDeploymentAliasRequest,
  }): CancelablePromise<DeploymentAliasResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/deployment-aliases/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Remove a named deployment alias.
   * Removes the mapping only; deployments and production traffic are unchanged.
   * @returns void
   * @throws ApiError
   */
  public static deleteDeploymentAlias({
    slug,
    name,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Lowercase DNS label for this app's stable revision alias.
     */
    name: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/deployment-aliases/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List deployments for an app.
   * Paged backwards (newest first) for the app identified by `slug`.
   * `next_before` is an opaque RFC3339Nano cursor from the last row in
   * the page; pass it as `before` to fetch older deployments. Unknown or
   * cross-account app slugs return the same IDOR-safe 404 surface as the
   * other app-scoped endpoints.
   *
   * @returns DeploymentListResponse A paginated list of deployments for the app.
   * @throws ApiError
   */
  public static listAppDeployments({
    slug,
    limit = 50,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Page size for this app (1–200; default 50).
     */
    limit?: number,
    /**
     * RFC3339Nano cursor from a previous response's next_before.
     */
    before?: string,
  }): CancelablePromise<DeploymentListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create a deployment.
   * Two content-types are accepted:
   * - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
   * - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
   * Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
   * The optional `workflows` array is plan-gated and schema-validated;
   * accepted definitions are persisted with the deployment and snapshotted
   * when a workflow run starts.
   *
   * @returns DeploymentResponse The deployment whose build has been accepted and queued.
   * @throws ApiError
   */
  public static createDeployment({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Either a prebuilt OCI image reference (`application/json`) or a source tarball upload (`multipart/form-data`). See the operation description for plan size caps.
     */
    requestBody: CreateDeploymentRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: image_egress_denied — registry is in RFC1918 / IMDS / link-local, or blocked egress range; or email_verification_required when the account email is unverified.`,
        413: `code: source_too_large`,
        422: `code: deploy_failed | image_not_found | image_manifest_invalid | build_oom | build_timeout | stateless_only_violation`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Fetch a deployment release summary and diff.
   * Returns the selected deployment, its immediately preceding release,
   * a stable field-level diff of non-secret release metadata, and the
   * deployment id the app rollback operation would currently target.
   * Unknown, cross-account, or slug/deployment-mismatch requests return
   * the same IDOR-safe 404 surface as the other app-scoped reads.
   *
   * @returns DeploymentSummaryResponse The release summary.
   * @throws ApiError
   */
  public static getAppDeploymentSummary({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DeploymentSummaryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{id}/summary',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Fetch the latest deployment for an app.
   * Returns the app's newest deployment by `created_at DESC`. The app slug
   * and deployment are resolved within the authenticated account; an
   * unknown or cross-account app and a never-deployed app all return the
   * same IDOR-safe 404 surface.
   *
   * @returns DeploymentResponse The latest deployment.
   * @throws ApiError
   */
  public static getLatestAppDeployment({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/latest',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create a developer deployment from a complete source snapshot or delta.
   * Transport used only for ad-hoc developer environments created by
   * `gregale dev`. With an empty `dev_source_base`, `source` is a complete
   * source archive. With a base revision, `source` contains changed entries
   * and `dev_source_deleted` removes paths from the cached base.
   *
   * The cache is account/app scoped, node-local, and disposable. apid
   * reconstructs and verifies a complete archive before applying the same
   * source-root, stateful-shape, secret-scan, Dockerfile, function, and
   * enqueue gates as an ordinary source deployment. A missing base returns
   * 409 `dev_source_base_missing`; clients retry the target as a complete
   * snapshot. Older servers safely return 404 on this distinct route.
   *
   * @returns DeploymentResponse The reconstructed developer deployment whose build has been accepted and queued.
   * @throws ApiError
   */
  public static deployDevSource({
    slug,
    formData,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    formData: {
      /**
       * Complete tar.gz when dev_source_base is absent; otherwise a tar.gz of changed entries.
       */
      source: Blob;
      /**
       * Canonical cached source revision. Omit for a complete snapshot.
       */
      dev_source_base?: string;
      /**
       * Canonical revision of the complete source tree after reconstruction.
       */
      dev_source_target: string;
      /**
       * JSON string array of canonical archive paths removed since dev_source_base.
       */
      dev_source_deleted?: string;
      dockerfile?: boolean;
      runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313';
      handler?: string;
      /**
       * Startup readiness for this developer-source deployment, encoded as JSON with one HTTP path or standard gRPC health selector.
       */
      healthcheck?: string;
      source_root?: string;
      /**
       * Named environment scope read by the deployment; omitted uses default.
       */
      scope?: string;
      /**
       * Optional JSON workflow-definition array attached to this developer deployment.
       */
      workflows?: string;
    },
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments/dev-source',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: email_verification_required — verify the account email before deploying code or changing billing settings.`,
        404: `code: not_found`,
        409: `code: dev_source_base_missing. Retry with a complete source snapshot.`,
        413: `code: source_too_large`,
        422: `code: deploy_failed | image_not_found | image_manifest_invalid | build_oom | build_timeout | stateless_only_violation`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create a deployment from a Git source-ref (headless).
   * Headless deploy path (issue #739 / DEPLOY-PROV-4 / ADR-092).
   * Resolves the GitHub install bound to the caller's account,
   * fetches the (repo, ref) tarball via the githubd bridge, spools
   * it under the per-plan SourceTarballMaxMB cap, validates shape,
   * and enqueues a build (Kind=DeploymentKindGitHub) pinned to
   * the resolved 40-char commit SHA.
   *
   * Designed for CI runners: bearer token only, no GitHub env
   * vars required. Idempotency-Key collapses concurrent / retried
   * CI jobs into one build row.
   *
   * Distinct from the dashboard bind path (`POST
   * /v1/apps/{slug}/deployments` with a `source` multipart
   * upload) which goes through the browser + UI bind picker.
   *
   * @returns DeploymentResponse The source-ref deployment whose build has been accepted and queued.
   * @throws ApiError
   */
  public static createDeploymentFromSourceRef({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SourceRefDeployRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments/source-ref',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: invalid_ref | validation_failed. ref must be a valid 40-char
        SHA, branch, or tag. source_branch can only be paired with a full
        commit SHA.
        `,
        401: `code: unauthorized`,
        403: `code: email_verification_required — verify the account email before deploying code or changing billing settings.`,
        404: `No durable GitHub install bound to the caller's account
        (code: github_install_not_found).
        `,
        409: `code: source_ref_stale. source_branch no longer points at the
        requested immutable commit SHA.
        `,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `githubd unreachable or source-ref tarball fetch failed
        (code: source_ref_unavailable). Retry in ~30s.
        `,
      },
    });
  }
  /**
   * @deprecated
   * Create a deployment from a CLI-uploaded local tarball (zero-config).
   * Zero-config deploy path (issue #961 / Mega-A PR-1, ADR-115).
   * This multipart endpoint is deprecated for new clients. Use the
   * resumable upload session endpoints under `/v1/uploads` for progress,
   * retry-safe chunking, and commit deduplication; this route remains
   * available during the migration window.
   * The CLI uploads a gzipped tar via the `tarball` form field and
   * an optional informational `{repo, ref}` JSON sidecar. The CLI
   * binary is the trust root: apid does NOT consult
   * `github_installations` and does NOT attempt a server-side git
   * fetch.
   *
   * Distinct from the source-ref path
   * (`POST /v1/apps/{slug}/deployments/source-ref`, ADR-092) which
   * resolves the GitHub install and pins the tarball to a 40-char
   * SHA. The source-ref handler is unchanged; this is a parallel
   * trust path for first-deploy customers without the GitHub App
   * installed.
   *
   * Wire shape:
   * multipart/form-data with two fields:
   * - `tarball` (required): the gzipped tar, capped at the
   * per-plan `SourceTarballMaxMB`.
   * - `sidecar` (optional): JSON `{repo, ref}` recorded on
   * the build row for provenance only. The build pipeline
   * does NOT use the sidecar to fetch upstream — the tarball
   * bytes are the build source, and the sidecar is purely
   * informational. Operators relying on source-pinning MUST
   * use the source-ref path instead.
   *
   * Lifecycle (issue #1182 fix): the refactored zero-config
   * CLI path runs `POST /v1/apps` (CreateApp) BEFORE this endpoint,
   * so a brand-new slug gets a 201 from CreateApp and a 202 from
   * this endpoint. A direct hit on this endpoint with a slug that
   * has never been created returns 404 — pre-#1182 zero-config
   * customers hit this with "no such app"; the fix folds the
   * path through CreateApp so the slug always exists by the time
   * this endpoint is reached.
   *
   * Audit kind: `deploy.local_tarball` (distinct from
   * `deploy.source_ref`).
   *
   * Every response carries the RFC 9745 `Deprecation` date,
   * RFC 8594 `Sunset` date, and RFC 8288 `Link` relations. Clients
   * should migrate to `POST /v1/uploads` before the sunset date.
   *
   * @returns DeploymentResponse The local-tarball deployment whose build has been accepted and queued.
   * @throws ApiError
   */
  public static createDeploymentFromSourceTarball({
    slug,
    formData,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    formData: {
      tarball: Blob;
      sidecar?: SourceTarballDeployRequest;
    },
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments/source-tarball',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      formData: formData,
      mediaType: 'multipart/form-data',
      errors: {
        400: `code: validation_failed. Missing \`tarball\` field, malformed
        sidecar JSON, or invalid tarball shape.
        `,
        401: `code: unauthorized`,
        403: `code: email_verification_required — verify the account email before deploying code or changing billing settings.`,
        404: `code: not_found. The slug does not exist OR belongs to
        another account (loadAppAndPreflight's IDOR silent-404 —
        apid deliberately returns the same shape for both cases
        to avoid leaking the existence of other customers'
        apps). The refactored zero-config CLI path (issue #1182)
        runs CreateApp before this endpoint, so a slug should
        always exist by the time the request lands here; a 404
        on the CLI path is the symptom of a misconfigured
        --name pointing at a row the caller doesn't own.
        `,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Roll back to the previous deployment, or to a specific historical deployment.
   * Without a request body, rolls back to the most-recent superseded
   * deployment (the pre-#976 behaviour).
   *
   * With `target_deployment_id` in the body, rolls back to the
   * named deployment. The id must belong to this app and the row
   * must be superseded or live with zero traffic. Rolling back to the
   * already-current live deployment is rejected (409
   * `rollback_target_already_live`). A target whose snapshot has
   * been garbage-collected is rejected (409
   * `rollback_target_snapshot_expired`).
   * With both `target_deployment_id` and `expected_current_deployment_id`,
   * starts an exact checked rollback. The expected deployment must still
   * serve all traffic with no active rollout in this scope. A 202 response
   * includes `rollback_operation`, confirming durable intent. Readiness,
   * artifact and API contract checks precede a zero-traffic activation;
   * fresh binding evidence is checked at the traffic transaction. Service
   * completion also waits for the existing gateway ACK and drain handoff.
   * Stored binding enforcement requires this exact workflow.
   *
   * @returns DeploymentResponse The selected deployment and, for an exact checked request, its accepted durable rollback operation. Acceptance does not imply completion.
   * @throws ApiError
   */
  public static rollbackApp({
    slug,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    requestBody?: RollbackRequest,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/rollback',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: rollback_target_not_found — the named target_deployment_id does not match any deployment of this app (or does not exist).`,
        409: `code: no_rollback_target | rollback_target_already_live | rollback_target_snapshot_expired — rollback was rejected; see the response body for the specific code and detail.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read an exact historical rollback operation.
   * Read-only progress for a pinned deployment pair. Complete includes a committed routing audit; service completion also requires the matching handoff to finish. Blocked operations retry fresh evidence without choosing another deployment.
   * @returns RollbackOperation Durable rollback progress.
   * @throws ApiError
   */
  public static getRollbackOperation({
    slug,
    operation,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the accepted historical rollback operation.
     */
    operation: string,
  }): CancelablePromise<RollbackOperation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/rollbacks/{operation}',
      path: {
        'slug': slug,
        'operation': operation,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Operator manual rollout recovery (SAFE-RELEASES-R, issue
   * The operator escape hatch for a stuck canary rollout. Three
   * closed-set actions:
   *
   * - `advance`: bumps `canary_step` by 1, stamps
   * `canary_step_started_at = now()`, and redistributes the
   * traffic-split (largest-remainder Σ = 100). Requires the
   * rollout to be stuck (`canary_step_started_at` older than
   * the canned 30-minute stuck-after window). On a healthy
   * rollout the handler returns 409 `rollout_not_stuck`
   * with the suggestion "use --action promote instead".
   *
   * - `promote`: short-circuits the rollout to
   * `canary_step = canary_total_steps` and
   * `rollout_state = 'complete'`, with `traffic_percent = 100`
   * on the in-flight row + 0 on the siblings. No stuck-check;
   * this is the operator's "I'm sure, ship it" path.
   *
   * - `abort`: ordinary canaries transition synchronously to
   * `rollout_state = 'aborted'`. A zero-step service rollout instead
   * records a durable abort request and returns 202; schedd restores
   * the predecessor route, waits for gateway acknowledgement and
   * candidate request drain, then finalises the abort. `advance` and
   * `promote` are invalid for service rollouts. Emits a
   * `deploy.rolled_back` audit row for the operator intent.
   *
   * Returns the post-transition Deployment + the audit row id
   * so the operator's terminal can echo `audit_id=…`. Plan-tier
   * gated to Pro+ (Hobby / Free get 403
   * `plan_traffic_split_not_allowed`).
   *
   * For an exact canary abort, supply both `deployment_id` and
   * `expected_predecessor_deployment_id`. The older predecessor must
   * remain live and serving in the same app/scope. If its stored binding
   * release policy enforces verification, recovery checks that exact
   * recipient's fresh evidence and rechecks policy revisions and expiry
   * inside the recovery transaction. Missing or changed evidence leaves
   * traffic unchanged. Success restores the predecessor to 100 percent,
   * aborts the selected canary, and includes an exact recovery receipt.
   * For an exact service abort, the pinned predecessor may remain live
   * at zero weight after cutover. The response is 202 with a
   * `service_recovery` receipt confirming the durable request only.
   * APID checks that recipient and ready service capacity before publishing
   * routes; schedd then completes gateway acknowledgement and request drain.
   * GET the exact deployment to observe `service_rollout_handoff` progress
   * and bounded `bindings_check` blockers. Restarted workers resume the same
   * request and never substitute a different predecessor.
   *
   * @returns RolloutTransitionResponse The post-recovery deployment + audit row id.
   * @throws ApiError
   */
  public static recoverRollout({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: RecoverRolloutRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<RolloutTransitionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/rollouts/recover',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Canary traffic splitting is unavailable on the Hobby / Free plan.`,
        404: `code: not_found`,
        409: `one of: rollout_not_stuck | rollout_state_invalid`,
        422: `action ∉ {advance, promote, abort} (closed-set check).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List deployments across all apps on the account.
   * Paged backwards (newest first). `next_before` is an opaque cursor
   * (RFC3339Nano of the `created_at`); pass it on the next request to
   * page backwards. Empty `next_before` means end of list.
   *
   * @returns DeploymentListResponse A paginated list of deployments.
   * @throws ApiError
   */
  public static listDeployments({
    limit = 50,
    before,
  }: {
    /**
     * Page size (1–200, default 50).
     */
    limit?: number,
    /**
     * RFC3339Nano cursor from a previous response's `next_before`.
     */
    before?: string,
  }): CancelablePromise<DeploymentListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments',
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List the latest deployment for each app on the account.
   * Returns at most one deployment for every non-deleted app owned by the
   * authenticated account. Items are ordered newest first by `created_at`,
   * with deployment ID as the stable tie-breaker.
   *
   * @returns LatestDeploymentsByAppResponse The latest deployment for each deployed app.
   * @throws ApiError
   */
  public static listLatestDeploymentsByApp(): CancelablePromise<LatestDeploymentsByAppResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/latest-by-app',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Fetch one deployment.
   * @returns DeploymentResponse The deployment.
   * @throws ApiError
   */
  public static getDeployment({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Set the per-deployment cold-wake floor.
   * Update the deployment's min_instances (issue #557 closure /
   * ADR-072). The only mutable field on a deployment post-create;
   * image / digest / overrides / sidecars stay immutable (a new
   * deployment is the canonical way to change them). Pass
   * min_instances=0 to inherit from the parent app's floor.
   * Validated against the parent app's plan MaxMinInstances cap.
   *
   * @returns DeploymentResponse The updated deployment.
   * @throws ApiError
   */
  public static updateDeploymentMinInstances({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: {
      /**
       * Per-deployment cold-wake floor override. 0 = inherit from parent app.
       */
      min_instances: number;
    },
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/deployments/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `\`400 Bad Request\` — request body failed JSON decode or
        \`min_instances\` was outside the inclusive \`[0, plan_cap]\`
        range. Stable code \`min_instances_invalid\`.
        `,
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `\`422 Unprocessable Entity\` — request was syntactically valid
        but the parent app's plan refuses the override (e.g. a
        Free app PATCHing \`min_instances=1\`). Stable code
        \`plan_min_instances_not_allowed\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Soft-delete a deployment.
   * ADR-124 deployment queue controls — soft-delete one deployment
   * row. Status is intentionally untouched (admin audit trail).
   * Live deployments return 409 with the cancel-live hint pointing
   * at `gregale deploys rollback`.
   *
   * @returns any Soft-deleted.
   * @throws ApiError
   */
  public static clearDeployment({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/deployments/{id}',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
        409: `Live deployments cannot be cleared.`,
      },
    });
  }
  /**
   * Reorder a pending deployment.
   * ADR-124 deployment queue controls — update the priority of a
   * still-pending deployment. 0 = deploy immediately (top of
   * queue), 100 = FIFO default, 1000 = background rebuild.
   * Plan-gated (Hobby/Pro/Scale only); Free returns 402
   * `plan_reorder_disabled`. 409 if the deployment has already
   * moved off the pending queue.
   *
   * @returns any Reordered.
   * @throws ApiError
   */
  public static reorderDeployment({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: {
      priority: number;
    },
  }): CancelablePromise<{
    id?: string;
    priority?: number;
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/deployments/{id}/reorder',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        402: `Plan does not allow reorder.`,
        409: `Row is past the pending queue.`,
      },
    });
  }
  /**
   * Cancel a deployment.
   * ADR-124 deployment queue controls — flip a deployment in
   * {pending, building, imaging, snapshotting} to "cancelled"
   * and cascade-cancel its in-flight builds. Live deployments
   * return 409 `deployment_cancel_live_forbidden` with the
   * rollback hint. Optional reason: user | auto_quota |
   * auto_health | system.
   *
   * @returns any Cancelled.
   * @throws ApiError
   */
  public static cancelDeployment({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody?: CancelDeploymentRequest,
  }): CancelablePromise<{
    id?: string;
    status?: string;
    cancelled_at?: string;
    cancel_reason?: string;
    cancelled_builds?: Array<string>;
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments/{id}/cancel',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `code: not_found`,
        409: `Live deployment cannot be cancelled.`,
      },
    });
  }
  /**
   * Bulk soft-delete terminal-but-not-current deployments.
   * ADR-124 deployment queue controls — bulk soft-delete rows
   * in {superseded, failed, cancelled} older than the cutoff
   * (default 168h). Plan-gated (Free returns 402
   * `plan_reorder_disabled`). Retention
   * cap enforced inside the store so INV 3 stays satisfied.
   *
   * @returns ClearObsoleteReport Cleared.
   * @throws ApiError
   */
  public static clearObsoleteDeployments({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody?: {
      /**
       * Go duration (e.g. 168h).
       */
      older_than?: string;
    },
  }): CancelablePromise<ClearObsoleteReport> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments/clear-obsolete',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        402: `code: plan_reorder_disabled — this plan does not include deployment queue controls.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Set the per-deployment traffic-split weight.
   * Update the deployment's traffic_percent (issue #556 PR-A).
   * PR-A uses the zero-siblings rebalance form: setting row R's
   * traffic_percent to N forces every other live row in the same
   * app to 0, keeping Σ = 100 by construction. Pro/Scale only —
   * Free/Hobby are rejected at 403 `plan_traffic_split_not_allowed`.
   * Range-check [0, 100] is enforced at the handler (422
   * `invalid_traffic_percent`). The Σ invariant is asserted
   * post-write as a defensive backstop (409
   * `traffic_percent_sum_invalid`).
   * An optional expected_serving_deployment_id is checked under the
   * same live-row locks before rebalance. A stale expectation returns
   * 409 `traffic_serving_changed` without changing traffic.
   * An in-flight managed canary owns the app's traffic weights; direct
   * traffic changes during `pending` or `rolling_out` return 409
   * `traffic_change_during_canary` without changing traffic.
   *
   * @returns DeploymentResponse The updated deployment with the new traffic_percent.
   * @throws ApiError
   */
  public static updateDeploymentTraffic({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateDeploymentTrafficRequest,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/deployments/{id}/traffic',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `\`400 Bad Request\` — request body failed JSON decode.
        `,
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — the account's plan refuses traffic
        splitting (Free / Hobby). Stable code
        \`plan_traffic_split_not_allowed\`.
        `,
        404: `code: not_found`,
        409: `\`409 Conflict\` — the post-write Σ invariant check tripped
        (\`traffic_percent_sum_invalid\`), the optional
        expected_serving_deployment_id was stale
        (\`traffic_serving_changed\`), or an in-flight managed canary
        owns traffic (\`traffic_change_during_canary\`). The canary and
        serving checks run before mutation; a sum-invariant failure
        rolls back all tentative updates.
        `,
        422: `\`422 Unprocessable Entity\` — \`traffic_percent\` was
        outside the inclusive \`[0, 100]\` range. Stable code
        \`invalid_traffic_percent\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Promote a deployment after an atomic bindings check.
   * Available on all supported plans. Evaluate the exact live, materialized candidate using
   * the bindings preflight policy, then compare its binding/configuration,
   * probe and runtime revision inside the traffic transaction. Blockers,
   * expired evidence or changed observations return 409 without changing
   * traffic. No probes or restarts are scheduled. Queue/outbound probe
   * coverage requires an explicit allow_unsupported waiver and remains
   * partial. A successful receipt confirms the applied policy and check.
   * An optional serving expectation is checked under the traffic locks.
   * An already promoted target still requires a passed bindings check;
   * it returns an idempotent receipt without requiring the previous
   * deployment to remain at 100%. Active managed canaries cannot be bypassed.
   * This dedicated route prevents older servers from ignoring the bindings gate.
   * For require_application_ack use promote-with-application-ack so an older
   * server cannot silently ignore the new policy field.
   *
   * @returns BindingPromotionResponse Promoted deployment and the server-enforced bindings check.
   * @throws ApiError
   */
  public static promoteDeploymentWithBindings({
    id,
    requestBody,
  }: {
    /**
     * Deployment UUID in canonical or 32-hex form.
     */
    id: string,
    requestBody: BindingPromotionRequest,
  }): CancelablePromise<BindingPromotionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/deployments/{id}/promote',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid JSON request.`,
        401: `code: unauthorized`,
        403: `Traffic promotion is not allowed by the account plan or token scope.`,
        404: `code: not_found`,
        409: `Bindings check failed or changed, target is unavailable, serving expectation changed, or a managed canary owns traffic. Binding failures include a structured bindings_check report.`,
        422: `Invalid policy duration or deployment identifier.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        500: `The check or transaction could not complete; traffic is unchanged.`,
        503: `The binding catalogs cannot enforce the promotion fence.`,
      },
    });
  }
  /**
   * Promote after atomic bindings and application acknowledgement checks.
   * Always require current version-bound application acknowledgements from
   * every authorized resident workload of PostgreSQL and object-storage
   * bindings in the candidate scope, including a resident candidate target
   * for each binding. Missing, stale, failed, disabled or unknown receipts
   * block promotion. Application receipts are self-attestations, distinct
   * from connectivity probes and guest projection/signal outcomes.
   * require_application_ack is forced true even if the request omits it or
   * supplies false. All normal bindings checks, permissions and atomic
   * traffic fences also apply. Changes to credentials, authorized workload
   * rosters, reload support or receipts invalidate the check at the write.
   * Use this route for strict promotion; older servers return 404 before
   * changing traffic. Never fall back to the ordinary promotion route.
   *
   * @returns BindingPromotionResponse Traffic receipt confirming bindings checks and required current application acknowledgements.
   * @throws ApiError
   */
  public static promoteDeploymentWithApplicationAck({
    id,
    requestBody,
  }: {
    /**
     * Candidate UUID for strict adoption promotion; canonical and compact hexadecimal forms are accepted.
     */
    id: string,
    requestBody: BindingPromotionRequest,
  }): CancelablePromise<BindingPromotionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/deployments/{id}/promote-with-application-ack',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `The strict promotion policy body could not be decoded.`,
        401: `code: unauthorized`,
        403: `Strict adoption promotion requires an eligible account and deployment write permission.`,
        404: `code: not_found`,
        409: `The strict binding/application policy failed or changed, or the candidate, serving expectation or managed canary prevents promotion. A bindings_check report accompanies adoption failures.`,
        422: `The strict promotion request supplied an invalid evidence age or deployment expectation.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        500: `Application adoption promotion could not finish its observation read or traffic transaction.`,
        503: `Strict promotion cannot share a transaction fence across its binding catalogs and traffic backend.`,
      },
    });
  }
  /**
   * Advance one persisted canary stage.
   * Atomically advances the deployment's canary ladder by one stage.
   * The caller supplies the canary_step it observed; APID resolves the
   * next percentage from the deployment's persisted preset and rejects
   * stale workers with `409 canary_step_conflict`. The state transition,
   * sibling traffic rebalance, terminal promotion, and deployment audit
   * row are committed together. Pro/Scale only — Free/Hobby are rejected
   * at 403 `plan_traffic_split_not_allowed`.
   * Enforced route gates require complete, current, satisfied evidence for
   * this candidate under policy, intent and capture locks. Missing or stale
   * evidence durably requests a fresh check without increasing traffic.
   * A blocked gate returns 409 `route_gate_blocked` with reason codes in detail.
   *
   * @returns CanaryAdvanceResponse The atomically advanced deployment and audit row id.
   * @throws ApiError
   */
  public static advanceDeploymentCanary({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: AdvanceCanaryRequest,
  }): CancelablePromise<CanaryAdvanceResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/deployments/{id}/canary/advance',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Plan tier gate tripped (Hobby / Free).`,
        404: `code: not_found`,
        409: `Stale canary step, invalid rollout state, traffic sum conflict or enforced route gate blocked.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Stream build logs (SSE).
   * Server-Sent Events stream of build logs. `follow=1` holds the
   * connection open until the build completes.
   *
   * ADR-117: the stream also publishes one `event: stage` frame
   * per named pipeline stage the customer's deploy passes
   * through. The frame shape is:
   *
   * event: stage
   * data: {"name":"<StageName>","started_at":"<RFC3339Nano>","duration_ms":<int64>,"status":"in_progress"|"completed"|"failed"[,"reason":"<string>"]}
   *
   * `name` is one of the closed 6-stage vocabulary:
   * `source_download`, `dependency_restore`, `image_build`,
   * `security_scan`, `snapshot_prepare`, `readiness`. The CLI
   * renders the stream as a live ticker on a TTY (ANSI cursor-up
   * redraw) with a static one-line-per-frame fallback when
   * stdout is not a TTY / `--json` is set / `NO_COLOR` is
   * non-empty. Customers wiring their own consumer should treat
   * the frame as additive — the existing `event: log` /
   * `event: status` / `event: end` shapes are unchanged.
   *
   * @returns any A text/event-stream of build log entries, terminated by an empty SSE frame when the build finishes.
   * @throws ApiError
   */
  public static streamDeploymentLogs({
    id,
    beforeSeq,
    limit,
    follow = 0,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Stream cursor — return only log entries with seq strictly less than this value.
     */
    beforeSeq?: number,
    /**
     * Maximum number of log entries to return in the initial burst before streaming.
     */
    limit?: number,
    /**
     * If 1, hold the connection open and stream new build log entries as they arrive.
     */
    follow?: 0 | 1,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/logs',
      path: {
        'id': id,
      },
      query: {
        'before_seq': beforeSeq,
        'limit': limit,
        'follow': follow,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List deployment audit timeline.
   * Returns the deployment_audit rows for one deployment in
   * reverse-chronological order (issue #976 / ADR-122 /
   * SAFE-RELEASES-E.2 + production-leveling Stream A).
   *
   * The wire surface is a paginated JSON list
   * (ListDeploymentAuditResponse); for the SSE-streaming
   * variant of the build log itself see
   * `/v1/deployments/{id}/logs`.
   *
   * IDOR posture: the handler resolves the deployment ID
   * via `pkg/state.DeploymentByID` + `pkg/state.AppByID`
   * + account match BEFORE returning rows. A
   * cross-account probe returns 404 (no
   * account-existence leak).
   *
   * Limit defaults to 50, clamped to [1, 500]; the
   * server-applied limit is echoed back in the response so
   * a paging consumer can distinguish "limit was clamped"
   * from "no more rows" (both yield Items of length <
   * limit).
   *
   * @returns ListDeploymentAuditResponse Paginated deployment_audit rows in reverse-chronological order.
   * @throws ApiError
   */
  public static listDeploymentAudit({
    id,
    limit = 50,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Maximum number of audit rows to return. Clamped to [1, 500]; the server-applied limit is echoed back in the response.
     */
    limit?: number,
  }): CancelablePromise<ListDeploymentAuditResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/audit',
      path: {
        'id': id,
      },
      query: {
        'limit': limit,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Deployment row missing or cross-account probe (no account-existence leak).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get per-deploy grype scan.
   * Returns the per-deploy grype CVE scan payload (issue #464 /
   * ADR-055). The scan runs on the per-app layer ext4 in imaged's
   * deploy-complete path (after `SetDeploymentRootfs`, before the
   * pending→snapshotting transition) and lands on the
   * `deployments` row.
   *
   * Status field is the closed enum:
   * - `complete` — full payload (SeverityCounts + Vulnerabilities).
   * - `failed` — payload carries `error` only; SeverityCounts is
   * all-zero, Vulnerabilities is nil. Rendered as the
   * "scan failed" chip on the dashboard.
   * - `skipped` — pre-feature backfill row
   * (migrations/00135 stamps `scan_status='skipped'` on every
   * pre-#464 row). Payload carries the reason sentinel.
   *
   * A 404 is returned when:
   * - the deployment row does not exist,
   * - the deployment belongs to a different account (IDOR-safe;
   * no account-existence leak),
   * - no scan has run yet (the deploy is still mid-pipeline or
   * the row predates #464 entirely).
   *
   * @returns ScanResult The typed scan payload. The shape mirrors the `deployments.scan_result` jsonb column (with the Status re-pinned from the authoritative `scan_status` column).
   * @throws ApiError
   */
  public static getDeploymentScan({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<ScanResult> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/scan',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Deployment row missing, cross-account probe, or scan has not run yet.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get per-deploy image-layer secret scan.
   * Returns the per-deploy secret-scan audit row (PR-A /
   * ADR-101). The scan runs on the per-app ext4 in imaged's
   * deploy-complete path (after `SetDeploymentRootfs`, before
   * the pending→snapshotting transition) using the same
   * `pkg/secretscan` engine the apid source-tree rejection
   * path uses — same patterns, same providers, same Severity
   * table, same snippet policy.
   *
   * Status field is the closed enum:
   * - `complete` — image layer walked clean; `findings=[]`.
   * Stamped on every scan (clean OR hit) so the dashboard
   * renders the audit row immediately after the build.
   * - `complete_with_redactions` — at least one finding
   * landed in the audit row; `error_code =
   * 'image_secret_detected'` on the deployment. The
   * deploy's pending→snapshotting transition does NOT
   * fire.
   *
   * A 404 is returned when:
   * - the deployment row does not exist,
   * - the deployment belongs to a different account
   * (IDOR-safe; no account-existence leak),
   * - no scan has run yet (the deploy is still
   * mid-pipeline or the row predates PR-A entirely).
   *
   * Each finding carries a `layer` label that attributes the
   * finding to the per-walk source (`app` for the main image,
   * `sidecar-<slug>` for each sidecar). Pre-PR-A rows
   * (rejected source-tree bytes via the v2 422 path) carry
   * `layer` empty or absent.
   *
   * @returns SecretScanResult The typed secret-scan payload. Shape mirrors the `deployments.secret_findings` jsonb column.
   * @throws ApiError
   */
  public static getDeploymentSecretScan({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<SecretScanResult> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/secret-scan',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Deployment row missing, cross-account probe, or secret scan has not been stamped for this deploy yet (pre-PR-A rows return 404 because the \`secret_findings\` jsonb has never been written).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Inspect immutable runtime base identity.
   * Read-only artifact evidence for Gregale-managed function runtimes (ADR-682).
   * Older artifacts return unknown rather than inferring their base from builder
   * provenance. Published candidates are limited to the same family and architecture,
   * at most 50 newest publications. Publication does not establish upgrade compatibility.
   * No VM is booted and no deployment, traffic or environment setting is changed.
   *
   * @returns DeploymentRuntimeResponse Current binding and published candidates.
   * @throws ApiError
   */
  public static getDeploymentRuntime({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DeploymentRuntimeResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/runtime',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Runtime evidence is temporarily unavailable.`,
      },
    });
  }
  /**
   * Preview a runtime base change without applying it.
   * Compares exact published component identities (ADR-682). This planning-only
   * response cannot authorize an update. Runtime family changes, architecture changes
   * and unknown current provenance block the plan. Same-family changes still require
   * native qualification, a rebuilt candidate, fresh readiness and guarded rollout.
   * Execution is unavailable; publication order does not prove a newer interpreter
   * patch or a compatible update. Existing health history is advisory evidence.
   *
   * @returns RuntimeUpgradePreviewResponse Read-only component comparison and required steps.
   * @throws ApiError
   */
  public static previewRuntimeUpgrade({
    id,
    target,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Exact published runtime release identity to compare.
     */
    target: string,
  }): CancelablePromise<RuntimeUpgradePreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/runtime/upgrade-preview',
      path: {
        'id': id,
      },
      query: {
        'target': target,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `The runtime preview could not read its evidence.`,
      },
    });
  }
  /**
   * Get per-deploy closed-stage summary (ADR-117 follow-up).
   * Returns the closed 6-stage summary for a deployment. Companion
   * to `/v1/deployments/{id}/logs` (which streams `event: stage`
   * frames during a live deploy) and `/v1/deployments/{id}` (which
   * returns the typed deployment row). This endpoint serves the
   * post-stream summary use case — `gregale deploys show <id>` and
   * the future dashboard widget.
   *
   * The body is the same JSON shape already stored on
   * `deployments.stage_state` (ADR-117, migration 00302). The
   * handler does NOT add a typed DTO — the column's jsonb IS the
   * wire. The closed vocabulary (`source_download` /
   * `dependency_restore` / `image_build` / `security_scan` /
   * `snapshot_prepare` / `readiness`) is enforced at the
   * database layer by `deployments_stage_state_current_check`,
   * so a malformed row would never reach the wire. The
   * `current` field is the stage the deploy is in right now;
   * `history` lists the closed rows in transition order
   * (oldest → newest), each carrying server-measured
   * `duration_ms` so the CLI / dashboard don't have to trust
   * a 2s-tick reconstruction.
   *
   * A 404 is returned when:
   * - the deployment row does not exist,
   * - the deployment belongs to a different account (IDOR-safe;
   * no account-existence leak).
   *
   * @returns any The raw `deployments.stage_state` jsonb. Shape: {current, current_started_at, history: [{name, started_at, ended_at, duration_ms, status, reason}]}.
   * @throws ApiError
   */
  public static getDeploymentStages({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<{
    current?: 'source_download' | 'dependency_restore' | 'image_build' | 'security_scan' | 'snapshot_prepare' | 'readiness';
    current_started_at?: string | null;
    history?: Array<{
      name?: 'source_download' | 'dependency_restore' | 'image_build' | 'security_scan' | 'snapshot_prepare' | 'readiness';
      started_at?: string | null;
      ended_at?: string | null;
      duration_ms?: number;
      status?: 'completed' | 'failed';
      reason?: string;
    }>;
  }> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/stages',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Deployment row missing or cross-account probe (IDOR-safe; never 403).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Retry a failed deployment from a named stage (ADR-117 production-ready follow-on C2).
   * Closes the production-ready gap exposed by ADR-117 §C4: a
   * deployment that fails partway is restorable via
   * `POST /v1/deployments/{id}/retry` with a `from_stage` field.
   * The deployment row is duplicated (NOT mutated); the new
   * row carries a fresh `stage_state.current` and a fresh
   * `stage_state.history` so the dashboard's stage-progression
   * timeline (and the CLI's `gregale deploys show <id>` summary)
   * reflects the retry as a separate event.
   *
   * The closed-6 vocabulary mirrors `state.AllStageNames`
   * (ADR-117); the API rejects unknown values with 400.
   * Empty strings are rejected for the same reason.
   *
   * Auth chain mirrors `POST /v1/apps/{slug}/deployments`:
   * `authLimited → requireMFA → requireScope(ScopesDeployWriteSurface)`.
   * Returns 202 Accepted with the new deployment row (same
   * shape as `POST /v1/apps/{slug}/deployments`).
   *
   * @returns DeploymentResponse The new deployment row (same shape as a fresh deploy).
   * @throws ApiError
   */
  public static retryDeployment({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: RetryDeploymentRequest,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/deployments/{id}/retry',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `\`400 Bad Request\` — \`from_stage\` is empty or
        not in the closed-6 vocabulary.
        `,
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — the caller's MFA factor or scope
        does not satisfy the deploy-write surface.
        `,
        404: `Retry requested on a missing or cross-account deployment (IDOR-safe; never 403).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get per-deployment preview URL (SAFE-RELEASES-C.2).
   * Returns the per-deployment preview URL shape
   * `deploy-{N}-{slug}.gregale.dev` that the cert allowlist will
   * mint under for a single deployment (issue #976 / ADR-122).
   * `N` is the per-app 1-based ordinal of the deployment row,
   * resolved from state.DeploymentOrdinal — the order is
   * stable so a previously-issued URL doesn't silently rot when
   * a later deploy lands.
   *
   * The `alive` field is the same predicate the cert allowlist
   * consults (state.Deployment.DeploymentPreviewActive):
   * `true` iff the deployment's status is in
   * `{pending, building, imaging, snapshotting, live}`. When
   * `alive=false` the handler returns 200 with `host=""` and
   * `url=""` so the dashboard renders a "preview closed" chip
   * without round-tripping again. When the per-deployment
   * preview zone is disabled (`wire.DeployWildcardSuffix == ""`)
   * the handler returns the same 200 + Alive=false shape so
   * envelopes stay stable across environments.
   *
   * A 404 is returned when:
   * - the deployment row does not exist,
   * - the deployment belongs to a different account
   * (IDOR-safe; no account-existence leak).
   *
   * @returns DeploymentPreviewURL The resolved per-deployment preview URL.
   * @throws ApiError
   */
  public static getDeploymentUrl({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DeploymentPreviewURL> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/url',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Deployment row missing or cross-account probe on the preview URL seam (IDOR-safe; never 403).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List builds (operator view).
   * Returns every build the authenticated account owns, ordered
   * started_at DESC (nulls last — queued builds stay at the
   * bottom of the first page). Optional ?app=<slug> narrows to
   * one app; optional ?status=<s> filters to the 4-value status
   * enum (queued|running|succeeded|failed; omit for any status).
   * Cursor pagination via ?before=<opaque token>; limit defaults
   * to 50, capped at 200.
   *
   * The response shape mirrors /v1/deployments: items + a
   * next_before cursor (empty when end of list). The cursor is
   * the opaque tuple `<rfc3339nano>|<id_hex>` of the LAST row
   * on this page — server-emitted, round-tripped verbatim. The
   * id tiebreaker makes the keyset deterministic for queued
   * tails (started_at IS NULL) and for sub-second collisions
   * on started_at. See ADR-091 §3.
   *
   * BuildResponse.started_at (the per-row wire field) is
   * RFC3339 (whole-second) for backward compatibility with
   * `GET /v1/builds/{id}`. The cursor's started_at segment is
   * RFC3339Nano (sub-second preserved) so the keyset
   * sub-second clause is reachable on rows whose started_at
   * falls in the same wall-clock second. The two are
   * deliberately different and the cursor's higher precision
   * is intentional.
   *
   * @returns BuildListResponse A page of builds (ordered started_at DESC, nulls last).
   * @throws ApiError
   */
  public static getBuilds({
    app,
    status,
    before,
    limit = 50,
  }: {
    /**
     * Filter to a single app slug. Cross-account slug renders 404.
     */
    app?: string,
    /**
     * Filter to a single status. Omit for any status.
     */
    status?: 'queued' | 'running' | 'succeeded' | 'failed',
    /**
     * Opaque cursor token from a previous response's `next_before`. Format: `<rfc3339nano>|<id_hex>` (pipe-separated). Empty started_at segment encodes a queued-tail cursor (the `<id_hex>` part alone). Round-trip verbatim — do NOT re-parse or re-encode.
     */
    before?: string,
    /**
     * Page size (default 50, capped at 200).
     */
    limit?: number,
  }): CancelablePromise<BuildListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/builds',
      query: {
        'app': app,
        'status': status,
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `\`400 Bad Request\` — bad cursor (not RFC3339), bad status
        filter (not one of queued|running|succeeded|failed), or
        bad limit (non-numeric / out of range). Stable code
        \`validation_failed\`.
        `,
        401: `code: unauthorized`,
        404: `\`404 Not Found\` — only raised when ?app=<slug> is set
        and the slug is unknown OR belongs to another account
        (uniform 404 so cross-account probes can't enumerate).
        Stable code \`app_not_found\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get build status.
   * Returns the lifecycle row for a build id — current status,
   * enqueued/started/finished timestamps, failure_class (when
   * status='failed'), and a server-computed duration_seconds
   * (only set when both started_at and finished_at are
   * populated). Companion to /v1/builds/{id}/provenance (post-
   * mortem export) and /v1/builds/{id}/sbom (post-mortem
   * blob); this one is the LIFECYCLE surface CI scripts call
   * to fail-fast on build error without scraping SSE.
   *
   * The status enum is `queued|running|succeeded|failed` (the
   * builds_status_check CHECK constraint; no 'cancelled' value).
   * failure_class, when present, is one of `oom|timeout|
   * user_error|infra` (the builds_failure_class_check CHECK
   * constraint). error_message is intentionally NOT in the
   * response — it lives on deployments; call GET
   * /v1/deployments/{id} for the per-failure string.
   *
   * @returns BuildResponse Build lifecycle row.
   * @throws ApiError
   */
  public static getBuild({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<BuildResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/builds/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `\`404 Not Found\` — the build id does not exist OR
        belongs to another account. Stable code
        \`build_not_found\`. The 404 surface is uniform so
        cross-account probes can't enumerate.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get build provenance.
   * Returns the ADR-038 `build_provenance` row for a single build.
   * Each successful build produces exactly one provenance row
   * (builderd's populator runs at the `markSucceeded` sites); the
   * row is the customer-visible "what ran?" record: buildkit /
   * railpack version, base / runner digests, source URL + commit
   * SHA, plan, builder node ID, and the build's started_at /
   * finished_at timestamps.
   *
   * A 404 with `code=build_provenance_not_found` is returned when
   * the build exists but no provenance row landed (the populator
   * logs a WARN inside builderd on a failed INSERT — the build
   * itself still succeeded). A 404 with `code=not_found` is
   * returned when no build row matches the id, or when the
   * build's owning app belongs to a different account.
   *
   * @returns BuildProvenanceResponse The build provenance row, with every field populated. Empty strings indicate a column the populator hasn't filled yet (the schema half of Phase 2 is in this PR; cosign / SBOM populate the rest in Phase 3).
   * @throws ApiError
   */
  public static getBuildProvenance({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<BuildProvenanceResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/builds/{id}/provenance',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Either the build row is missing, OR the build exists but the populator INSERT failed (code=build_provenance_not_found).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get build SBOM (CycloneDX JSON).
   * Streams the CycloneDX 1.5 SBOM JSON generated by `imaged`'s
   * post-build syft pass (issue #299). The body is the raw SBOM
   * document; the response content-type is
   * `application/vnd.cyclonedx+json` so external tooling
   * (cyclonedx-cli validate, jq with `@cyclonedx-json`, Grype's
   * `--from-file=cyclonedx-json`) can dispatch on header alone.
   *
   * Three failure modes, each with its own code so the SDK and
   * `faas build sbom <id>` can branch:
   *
   * - `404 not_found` — the build id does not exist, OR the build
   * belongs to another account (the handler returns the same
   * surface on both so account-existence isn't probeable).
   * - `503 build_sbom_unavailable` — the build row exists for this
   * account but imaged's syft populator did not persist a
   * CycloneDX document (pre-PR build, or best-effort WARN).
   * The CLI prints "no SBOM for this build" and exits 1; the
   * operator's job is to re-deploy imaged with Phase 3 active.
   * - `503 capacity_unavailable` — the SBOM exists but the file
   * was unreadable from disk (storage backend returned a
   * transient error). The customer retries once.
   *
   * @returns any The CycloneDX SBOM JSON document, served verbatim from the storage backend.
   * @throws ApiError
   */
  public static getBuildSbom({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/builds/{id}/sbom',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Either the build row is missing, OR the build exists but belongs to a different account (code=not_found on every negative path so account-existence isn't probeable).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `The SBOM populator has not produced an artefact for this build (code=build_sbom_unavailable), or the storage backend failed (code=capacity_unavailable).`,
      },
    });
  }
}
