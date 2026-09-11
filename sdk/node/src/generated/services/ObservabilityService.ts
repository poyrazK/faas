/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppLogDrainHealthResponse } from '../models/AppLogDrainHealthResponse.js';
import type { AppLogDrainResponse } from '../models/AppLogDrainResponse.js';
import type { CreateAppLogDrainRequest } from '../models/CreateAppLogDrainRequest.js';
import type { Trace } from '../models/Trace.js';
import type { UpdateAppLogDrainRequest } from '../models/UpdateAppLogDrainRequest.js';
import type { WakeTimelineResponse } from '../models/WakeTimelineResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ObservabilityService {
  /**
   * List runtime log destinations for this app.
   * @returns AppLogDrainResponse The configured log destinations. Authentication headers are always masked.
   * @throws ApiError
   */
  public static listAppLogDrains({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<AppLogDrainResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/log-drains',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_log_drains_not_allowed — the plan does not include customer runtime log destinations.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Export runtime logs to an external HTTP or OTLP endpoint.
   * The platform tails the existing per-instance runtime log ring and
   * forwards each line through a bounded queue. `http_json` sends a
   * provider-neutral JSON record; `otlp` sends an OTLP/HTTP JSON logs
   * envelope suitable for a collector or vendor OTLP endpoint. The URL
   * is SSRF-checked, and auth_header is sealed at rest and never echoed.
   *
   * @returns AppLogDrainResponse Log drain created.
   * @throws ApiError
   */
  public static createAppLogDrain({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAppLogDrainRequest,
  }): CancelablePromise<AppLogDrainResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/log-drains',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: app_log_drain_invalid — malformed log-drain kind, URL, or auth header.`,
        401: `code: unauthorized`,
        402: `code: plan_log_drains_not_allowed — the plan does not include customer runtime log destinations.`,
        403: `code: plan_log_drain_quota — per-app or per-account runtime log destination limit reached.`,
        404: `code: not_found`,
        409: `code: app_log_drain_invalid — malformed log-drain kind, URL, or auth header.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch one runtime log destination.
   * @returns AppLogDrainResponse The log destination. Authentication headers are masked.
   * @throws ApiError
   */
  public static getAppLogDrain({
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
  }): CancelablePromise<AppLogDrainResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/log-drains/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_log_drains_not_allowed — the plan does not include customer runtime log destinations.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Update a runtime log destination.
   * @returns AppLogDrainResponse The updated log destination.
   * @throws ApiError
   */
  public static updateAppLogDrain({
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
    requestBody: UpdateAppLogDrainRequest,
  }): CancelablePromise<AppLogDrainResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/log-drains/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: app_log_drain_invalid — malformed log-drain kind, URL, or auth header.`,
        401: `code: unauthorized`,
        402: `code: plan_log_drains_not_allowed — the plan does not include customer runtime log destinations.`,
        404: `code: not_found`,
        409: `code: app_log_drain_invalid — malformed log-drain kind, URL, or auth header.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete a runtime log destination.
   * @returns void
   * @throws ApiError
   */
  public static deleteAppLogDrain({
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
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/log-drains/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_log_drains_not_allowed — the plan does not include customer runtime log destinations.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch durable delivery health for a runtime log destination.
   * Returns a customer-safe delivery snapshot. Authentication headers and
   * raw transport errors are never returned. Empty timestamps mean that
   * the corresponding event has not happened yet.
   *
   * @returns AppLogDrainHealthResponse Durable log-drain delivery health.
   * @throws ApiError
   */
  public static getAppLogDrainHealth({
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
  }): CancelablePromise<AppLogDrainHealthResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/log-drains/{id}/health',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_log_drains_not_allowed — the plan does not include customer runtime log destinations.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List the canonical wake-timeline frames for one wake.
   * Oldest-first (forward narrative). Returns every typed
   * `wake.*` events row for the given wake_id: queue_accepted
   * → admitted → boot_started → boot_completed →
   * readiness_200 → proxy_first_byte. Build and deploy
   * failures (`wake.build_failed`, `wake.deploy_failed`,
   * `wake.boot_failed`) are joined in alongside the success
   * path so a single GET shows the whole lifecycle.
   *
   * The endpoint is a sub-resource of `/v1/apps/{slug}`;
   * auth and rate-limit share the §12 per-app budget with
   * logs/metrics/wake. Cross-account access 404s the
   * same way unknown slugs do (forge-proof: every row's
   * `data.app_id` is verified to match the resolved app).
   *
   * @returns WakeTimelineResponse Wake-timeline frames.
   * @throws ApiError
   */
  public static listWakeTimeline({
    slug,
    wakeId,
    since,
    limit = 200,
  }: {
    /**
     * App slug (lowercase, kebab-case; per-account unique).
     */
    slug: string,
    /**
     * The per-wake correlation handle minted by the schedd
     * engine (UUID v4 in production). The endpoint returns
     * every `wake.*` events row whose `data.wake_id`
     * matches — the partial index `events_wake_id_idx`
     * (migrations/00113) serves the read in O(frames)
     * regardless of the events table size.
     *
     */
    wakeId: string,
    /**
     * Only return rows with `at >= since` (RFC 3339).
     */
    since?: string,
    /**
     * Max frames to return. Silently capped at 1000.
     */
    limit?: number,
  }): CancelablePromise<WakeTimelineResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/wakes/{wake_id}/timeline',
      path: {
        'slug': slug,
        'wake_id': wakeId,
      },
      query: {
        'since': since,
        'limit': limit,
      },
      errors: {
        400: `Malformed query parameter on the wake-timeline read — \`since\` not RFC 3339 or \`limit\` out of range.`,
        401: `code: unauthorized`,
        404: `No such app (slug) or wake_id is unknown.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Retrieve a stored OpenTelemetry trace.
   * Issue #555. Returns the full span tree for a single trace_id
   * (32-hex, the same as the request's `wake_id`). The trace is
   * sourced from the gatewayd-public in-memory ring (24h
   * retention, 100k default cap). When no OTLP endpoint is set
   * the ring is the only source — when OTLP is set, the ring
   * still operates as the customer-facing query layer.
   *
   * Authentication: the `X-Faas-Trace-Auth` header must carry
   * the operator's observer token (env: `FAAS_TRACE_OBSERVER_TOKEN`).
   * The endpoint is gated even when the dashboard session cookie is
   * present — tracing is an operator surface, not a customer one.
   * An empty token disables the endpoint (returns 404).
   *
   * @returns Trace The trace tree.
   * @throws ApiError
   */
  public static getTrace({
    traceId,
  }: {
    /**
     * 32-hex W3C trace_id (or wake_id UUIDv7 hex form).
     */
    traceId: string,
  }): CancelablePromise<Trace> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/traces/{trace_id}',
      path: {
        'trace_id': traceId,
      },
      errors: {
        401: `Missing or wrong observer token. The endpoint is operator-
        gated; the customer-facing dashboard session does not
        grant access.
        `,
        404: `Trace not in the ring (never seen, or evicted by the 24h
        retention sweep / LRU cap).
        `,
      },
    });
  }
}
