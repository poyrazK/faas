/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BuildListResponse } from '../models/BuildListResponse.js';
import type { BuildProvenanceResponse } from '../models/BuildProvenanceResponse.js';
import type { BuildResponse } from '../models/BuildResponse.js';
import type { CreateDeploymentRequest } from '../models/CreateDeploymentRequest.js';
import type { DeploymentListResponse } from '../models/DeploymentListResponse.js';
import type { DeploymentResponse } from '../models/DeploymentResponse.js';
import type { ScanResult } from '../models/ScanResult.js';
import type { UpdateDeploymentTrafficRequest } from '../models/UpdateDeploymentTrafficRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DeploymentsService {
  /**
   * Create a deployment.
   * Two content-types are accepted:
   * - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
   * - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
   * Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
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
        403: `code: image_egress_denied — registry is in RFC1918 / IMDS / link-local, or blocked egress range.`,
        413: `code: source_too_large`,
        422: `code: deploy_failed | image_not_found | image_manifest_invalid | build_oom | build_timeout | stateless_only_violation`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Roll back to the previous deployment.
   * @returns DeploymentResponse The deployment that was created by rolling back to the previous version.
   * @throws ApiError
   */
  public static rollbackApp({
    slug,
    idempotencyKey,
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
      errors: {
        401: `code: unauthorized`,
        409: `code: no_rollback_target — there is no superseded deployment to roll back to.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * `traffic_percent_sum_invalid`) — structurally unreachable
   * with zero-siblings, but pinned by the test suite.
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
        409: `\`409 Conflict\` — post-write Σ invariant check tripped.
        Structurally unreachable with the zero-siblings rebalance
        form, but pinned by the test suite as a defensive
        backstop against future refactors.
        `,
        422: `\`422 Unprocessable Entity\` — \`traffic_percent\` was
        outside the inclusive \`[0, 100]\` range. Stable code
        \`invalid_traffic_percent\`.
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Stream build logs (SSE).
   * Server-Sent Events stream of build logs. `follow=1` holds the
   * connection open until the build completes.
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * Cursor pagination via ?before=<RFC3339Nano>; limit defaults
   * to 50, capped at 200.
   *
   * The response shape mirrors /v1/deployments: items + a
   * next_before cursor (empty when end of list). The cursor is
   * the started_at of the LAST row with a non-null started_at
   * on this page, so passing next_before never skips the
   * running/succeeded rows behind queued builds at the tail of
   * the previous page.
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
     * Cursor: fetch rows started strictly before this RFC3339Nano timestamp.
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        503: `The SBOM populator has not produced an artefact for this build (code=build_sbom_unavailable), or the storage backend failed (code=capacity_unavailable).`,
      },
    });
  }
}
