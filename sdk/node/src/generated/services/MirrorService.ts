/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateMirrorRuleRequest } from '../models/CreateMirrorRuleRequest.js';
import type { MirrorReplayBatchRequest } from '../models/MirrorReplayBatchRequest.js';
import type { MirrorReplayBatchResponse } from '../models/MirrorReplayBatchResponse.js';
import type { MirrorRuleListResponse } from '../models/MirrorRuleListResponse.js';
import type { MirrorRuleResponse } from '../models/MirrorRuleResponse.js';
import type { MirrorSummaryResponse } from '../models/MirrorSummaryResponse.js';
import type { UpdateMirrorRuleRequest } from '../models/UpdateMirrorRuleRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class MirrorService {
  /**
   * List every mirror rule for an app.
   * Per-app listing (issue #72 / ADR-124 PR-A2). At most
   * `Limits.MirrorTargetsPerApp` rows are returned (Free/Hobby = 0
   * blocked at the plan gate before reaching this surface; Pro = 1;
   * Scale = 3) — no pagination cursor in A2.
   *
   * @returns MirrorRuleListResponse Mirror rules for this app, ordered by created_at ASC.
   * @throws ApiError
   */
  public static listMirrorRules({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<MirrorRuleListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mirrors',
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
   * Create a mirror rule on an app.
   * Binds a source deployment to a mirror deployment for canary-shadow
   * comparison (issue #72 / ADR-125 / ADR-124 PR-A2). Both
   * deployments must be `live` and belong to the same app. Plan gate
   * fires 403 `plan_mirror_not_allowed` for Free/Hobby (cost: 1
   * mirror VM per request, billed per running second, capped at
   * `MirrorMaxLifetimeSeconds=5`). Per-app quota returns 422
   * `mirror_rule_quota_exceeded` once `Limits.MirrorTargetsPerApp`
   * is reached. The runtime dispatch (gateway goroutine, redaction,
   * schedd stamping) lands in PR-A3 — A2 stores the rule + emits
   * `mirror_rule.created` audit + pg_notify `kind="mirror"` so PR-A3
   * picks up the change within ~1s.
   *
   * @returns MirrorRuleResponse Mirror rule created.
   * @throws ApiError
   */
  public static createMirrorRule({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateMirrorRuleRequest,
  }): CancelablePromise<MirrorRuleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mirrors',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `\`403 Forbidden\` — Free/Hobby plan; mirror is Pro/Scale only.
        `,
        404: `code: not_found`,
        409: `\`409 Conflict\` — source or mirror deployment is not \`live\`.
        Code \`mirror_deployment_not_live\`.
        `,
        422: `\`422 Unprocessable Entity\` — \`percent\` out of [0, 100]
        (\`invalid_mirror_percent\`); source == mirror
        (\`mirror_source_target_same\`); source/mirror on different
        apps (\`mirror_cross_app_mismatch\`); per-app quota
        exhausted (\`mirror_rule_quota_exceeded\`).
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch one mirror rule by id.
   * Cross-account access returns 404 (silent), never 403 — the
   * IDOR posture matches traffic-split's deployment-id surface.
   *
   * @returns MirrorRuleResponse The mirror rule.
   * @throws ApiError
   */
  public static getMirrorRule({
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
  }): CancelablePromise<MirrorRuleResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mirrors/{id}',
      path: {
        'slug': slug,
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
   * Patch a mirror rule.
   * Patch semantics — pointer fields distinguish "absent" from
   * "set to zero". `percent=0` is a legal value (disable-without-
   * removing); a missing `percent` keeps the existing value. The
   * plan gate is intentionally NOT enforced on update: a Pro
   * customer's existing rule survives an upgrade to Hobby; the
   * reaper disables the rule on the next read window so the mirror
   * VM doesn't keep waking.
   *
   * @returns MirrorRuleResponse The updated mirror rule.
   * @throws ApiError
   */
  public static updateMirrorRule({
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
    requestBody: UpdateMirrorRuleRequest,
  }): CancelablePromise<MirrorRuleResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/mirrors/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `\`422 Unprocessable Entity\` — \`percent\` out of [0, 100]
        (\`invalid_mirror_percent\`).
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete a mirror rule.
   * Idempotent at the wire level — second DELETE returns 404.
   * Cascades to `mirror_invocation_results` rows via FK ON DELETE
   * CASCADE (PR-A1 migration 00384_mirror_rules.sql). Emits
   * `mirror_rule.deleted` audit + pg_notify `kind="mirror"`.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteMirrorRule({
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
      url: '/v1/apps/{slug}/mirrors/{id}',
      path: {
        'slug': slug,
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
   * Aggregate mirror drift counts over a window.
   * Read-only aggregate. Source: `mirror_invocation_results` rows
   * whose `completed_at >= now - window_seconds`. Returns:
   * total invocations, status diff count, schema diff count, body
   * diff count, mean/p99 latency delta, crash count. PR-A2 returns
   * zeros (PR-A1's ledger has no writers until A3 ships the
   * runtime); post-A3 this is the dashboard widget's data source.
   *
   * @returns MirrorSummaryResponse Aggregated drift counts.
   * @throws ApiError
   */
  public static getMirrorRuleSummary({
    slug,
    id,
    window = '1h',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Summary window. Defaults to 1h. Window is the inclusive trailing seconds the comparison ledger is aggregated over.
     */
    window?: '1h' | '24h' | '7d',
  }): CancelablePromise<MirrorSummaryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mirrors/{id}/summary',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'window': window,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `\`422 Unprocessable Entity\` — \`window\` is not one of
        \`1h | 24h | 7d\` (\`invalid_mirror_window\`).
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Replay a sanitized historical request corpus against the mirror deployment.
   * Queues 1–100 caller-sanitized JSON requests against this rule's mirror
   * deployment. Gregale strips credential, platform-owned, hop-by-hop, and
   * rule-configured redact headers again before enqueueing. GET/HEAD/OPTIONS
   * are accepted by default; POST/PUT/PATCH/DELETE require the explicit
   * `allow_unsafe_methods` acknowledgement. Source response bodies are never
   * uploaded: an optional SHA-256 expectation enables body comparison.
   *
   * @returns MirrorReplayBatchResponse Sanitized requests queued for mirror replay.
   * @throws ApiError
   */
  public static replayMirrorRequests({
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
    requestBody: MirrorReplayBatchRequest,
  }): CancelablePromise<MirrorReplayBatchResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mirrors/{id}/replay',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
