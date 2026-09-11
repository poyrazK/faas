/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppWebhookDeliveryListResponse } from '../models/AppWebhookDeliveryListResponse.js';
import type { AppWebhookResponse } from '../models/AppWebhookResponse.js';
import type { AppWebhookRetryDeliveryResponse } from '../models/AppWebhookRetryDeliveryResponse.js';
import type { CreateAppWebhookRequest } from '../models/CreateAppWebhookRequest.js';
import type { RotateAppWebhookSecretRequest } from '../models/RotateAppWebhookSecretRequest.js';
import type { RotateAppWebhookSecretResponse } from '../models/RotateAppWebhookSecretResponse.js';
import type { UpdateAppWebhookRequest } from '../models/UpdateAppWebhookRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class WebhooksService {
  /**
   * List outbound webhook subscriptions for this app.
   * @returns AppWebhookResponse The webhooks.
   * @throws ApiError
   */
  public static listAppWebhooks({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<AppWebhookResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/webhooks',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create a new outbound webhook subscription.
   * target_url is SSRF-guarded at write time (loopback / RFC1918
   * / link-local / metadata IPs are rejected unless
   * FAAS_EGRESS_ALLOW_LOOPBACK=1). webhook_secret arrives in
   * the body, is sealed with secretbox.SealBytes under the
   * APP_WEBHOOK namespace, and is NEVER returned in plaintext
   * — the response shape carries the masked constant. event_filter
   * is an optional allowlist; empty subscribes to every event in
   * the closed vocabulary.
   *
   * @returns AppWebhookResponse Webhook created.
   * @throws ApiError
   */
  public static createAppWebhook({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAppWebhookRequest,
  }): CancelablePromise<AppWebhookResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/webhooks',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: app_webhook_invalid — malformed webhook body (missing target_url, invalid retry_policy, out-of-vocabulary event, oversize secret, etc.) or invalid state transition (e.g. retry on a non-dead row).`,
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        403: `code: plan_webhook_quota — per-app or per-account webhook limit reached.`,
        404: `code: not_found`,
        409: `code: app_webhook_invalid — malformed webhook body (missing target_url, invalid retry_policy, out-of-vocabulary event, oversize secret, etc.) or invalid state transition (e.g. retry on a non-dead row).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch one webhook subscription by id.
   * @returns AppWebhookResponse The webhook.
   * @throws ApiError
   */
  public static getAppWebhook({
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
  }): CancelablePromise<AppWebhookResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/webhooks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Partial-update a webhook subscription.
   * Every field is optional. To rotate the secret in place, send
   * `webhook_secret` (the handler seals it; the response carries
   * the masked constant). To rotate via the dedicated endpoint,
   * use POST /webhooks/{id}/rotate-secret.
   *
   * @returns AppWebhookResponse The updated webhook.
   * @throws ApiError
   */
  public static updateAppWebhook({
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
    requestBody: UpdateAppWebhookRequest,
  }): CancelablePromise<AppWebhookResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/webhooks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: app_webhook_invalid — malformed webhook body (missing target_url, invalid retry_policy, out-of-vocabulary event, oversize secret, etc.) or invalid state transition (e.g. retry on a non-dead row).`,
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete a webhook subscription.
   * Cascades into app_webhook_deliveries via FK CASCADE — the
   * customer can no longer read the delivery history once the
   * subscription is gone. Mirrors the alert-rule DELETE shape.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteAppWebhook({
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
      url: '/v1/apps/{slug}/webhooks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Replace a webhook HMAC secret.
   * Seals the caller-supplied replacement and overwrites the row's
   * ciphertext in place. Supply the same value to the receiver. The
   * plaintext is never returned; the response carries the masked
   * constant and rotated_at only.
   *
   * @returns RotateAppWebhookSecretResponse Webhook secret rotation succeeded.
   * @throws ApiError
   */
  public static rotateAppWebhookSecret({
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
    requestBody: RotateAppWebhookSecretRequest,
  }): CancelablePromise<RotateAppWebhookSecretResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/webhooks/{id}/rotate-secret',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List recent deliveries for this webhook.
   * Cursor-paginated; most-recent-first. The delivery status
   * follows the closed vocabulary `pending | in_flight |
   * succeeded | failed | dead`. Dead rows can be re-armed with
   * POST /webhooks/{id}/deliveries/{did}/retry.
   *
   * @returns AppWebhookDeliveryListResponse The deliveries page.
   * @throws ApiError
   */
  public static listAppWebhookDeliveries({
    slug,
    id,
    pageSize = 50,
    pageToken,
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
     * Page size (1..100; default 50).
     */
    pageSize?: number,
    /**
     * Opaque cursor from the previous page's next_token.
     */
    pageToken?: string,
  }): CancelablePromise<AppWebhookDeliveryListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/webhooks/{id}/deliveries',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'page_size': pageSize,
        'page_token': pageToken,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Re-arm a dead delivery.
   * Resets the row to `pending` with attempt=0 +
   * next_attempt_at=now() so the dispatcher picks it up at the
   * next tick. Only valid on rows with status='dead'; returns
   * 400 app_webhook_invalid otherwise.
   *
   * @returns AppWebhookRetryDeliveryResponse The re-armed delivery.
   * @throws ApiError
   */
  public static retryAppWebhookDelivery({
    slug,
    id,
    did,
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
     * Delivery id (32-hex-char UUID).
     */
    did: string,
  }): CancelablePromise<AppWebhookRetryDeliveryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry',
      path: {
        'slug': slug,
        'id': id,
        'did': did,
      },
      errors: {
        400: `code: app_webhook_invalid — malformed webhook body (missing target_url, invalid retry_policy, out-of-vocabulary event, oversize secret, etc.) or invalid state transition (e.g. retry on a non-dead row).`,
        401: `code: unauthorized`,
        402: `code: plan_webhooks_not_allowed — the plan does not include outbound webhooks (Free today).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
