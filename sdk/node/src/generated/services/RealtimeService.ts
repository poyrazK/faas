/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateManagedRealtimeEndpointRequest } from '../models/CreateManagedRealtimeEndpointRequest.js';
import type { ManagedRealtimeEndpointResponse } from '../models/ManagedRealtimeEndpointResponse.js';
import type { UpdateManagedRealtimeEndpointRequest } from '../models/UpdateManagedRealtimeEndpointRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class RealtimeService {
  /**
   * List managed realtime endpoints for this app.
   * @returns ManagedRealtimeEndpointResponse The configured managed realtime endpoints.
   * @throws ApiError
   */
  public static listManagedRealtimeEndpoints({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<ManagedRealtimeEndpointResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create a managed realtime endpoint.
   * Registers the callback contract used by realtimed for connect,
   * message, and disconnect events. Callback and optional client
   * credentials are sealed at rest and never returned in plaintext.
   *
   * @returns ManagedRealtimeEndpointResponse Managed realtime endpoint created.
   * @throws ApiError
   */
  public static createManagedRealtimeEndpoint({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateManagedRealtimeEndpointRequest,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        403: `code: plan_realtime_quota — per-app or per-account managed realtime endpoint limit reached.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch one managed realtime endpoint.
   * @returns ManagedRealtimeEndpointResponse The managed realtime endpoint.
   * @throws ApiError
   */
  public static getManagedRealtimeEndpoint({
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
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Partially update a managed realtime endpoint.
   * @returns ManagedRealtimeEndpointResponse The updated managed realtime endpoint.
   * @throws ApiError
   */
  public static updateManagedRealtimeEndpoint({
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
    requestBody: UpdateManagedRealtimeEndpointRequest,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete a managed realtime endpoint.
   * @returns void
   * @throws ApiError
   */
  public static deleteManagedRealtimeEndpoint({
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
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
