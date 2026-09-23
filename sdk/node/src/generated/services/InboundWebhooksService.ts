/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateInboundWebhookEndpointRequest } from '../models/CreateInboundWebhookEndpointRequest.js';
import type { InboundWebhookEndpointResponse } from '../models/InboundWebhookEndpointResponse.js';
import type { InboundWebhookReceiptResponse } from '../models/InboundWebhookReceiptResponse.js';
import type { UpdateInboundWebhookEndpointRequest } from '../models/UpdateInboundWebhookEndpointRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class InboundWebhooksService {
  /**
   * List durable inbound webhook endpoints for this app.
   * @returns InboundWebhookEndpointResponse The configured endpoints. Public route tokens are never returned.
   * @throws ApiError
   */
  public static listInboundWebhookEndpoints({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<InboundWebhookEndpointResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/inbound-webhooks',
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
   * Create a provider-verified durable webhook endpoint.
   * Returns the public endpoint_url once. Gregale stores only a SHA-256
   * digest of its opaque token and an age/X25519-sealed provider signing
   * secret. Hobby, Pro, and Scale plans are supported.
   *
   * @returns InboundWebhookEndpointResponse Endpoint created; copy endpoint_url into the provider now.
   * @throws ApiError
   */
  public static createInboundWebhookEndpoint({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateInboundWebhookEndpointRequest,
  }): CancelablePromise<InboundWebhookEndpointResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/inbound-webhooks',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: inbound_webhook_invalid for malformed endpoint configuration/body or a missing Stripe event id; code: inbound_webhook_bad_signature when Stripe-Signature does not verify.`,
        401: `code: unauthorized`,
        402: `code: plan_inbound_webhooks_not_allowed — the plan excludes durable inbound webhook ingress.`,
        403: `code: plan_inbound_webhook_quota — the app or account endpoint cap is reached.`,
        404: `code: not_found`,
        409: `code: inbound_webhook_invalid for malformed endpoint configuration/body or a missing Stripe event id; code: inbound_webhook_bad_signature when Stripe-Signature does not verify.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
  /**
   * Fetch one durable inbound webhook endpoint.
   * @returns InboundWebhookEndpointResponse Endpoint metadata. The route token and signing secret are not returned.
   * @throws ApiError
   */
  public static getInboundWebhookEndpoint({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inbound webhook endpoint ID.
     */
    id: string,
  }): CancelablePromise<InboundWebhookEndpointResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/inbound-webhooks/{id}',
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
   * Change delivery, enabled state, or rotate the provider secret.
   * @returns InboundWebhookEndpointResponse Updated endpoint metadata.
   * @throws ApiError
   */
  public static updateInboundWebhookEndpoint({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inbound webhook endpoint ID.
     */
    id: string,
    requestBody: UpdateInboundWebhookEndpointRequest,
  }): CancelablePromise<InboundWebhookEndpointResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/inbound-webhooks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: inbound_webhook_invalid for malformed endpoint configuration/body or a missing Stripe event id; code: inbound_webhook_bad_signature when Stripe-Signature does not verify.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
  /**
   * Stop future ingress for this endpoint.
   * Already accepted invocation rows remain queued and deliverable.
   * @returns void
   * @throws ApiError
   */
  public static deleteInboundWebhookEndpoint({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inbound webhook endpoint ID.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/inbound-webhooks/{id}',
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
   * Verify and durably accept a provider webhook.
   * This route does not use a Gregale bearer key. The opaque URL and the
   * provider signature are the trust boundary. For Stripe, the exact raw
   * body is verified against Stripe-Signature. A 202 is returned only after
   * the deterministic invocation receipt commits. Provider retries return
   * the same receipt with duplicate=true.
   *
   * @returns InboundWebhookReceiptResponse Verified and durably accepted, or an already accepted provider retry.
   * @throws ApiError
   */
  public static receiveInboundWebhook({
    token,
    stripeSignature,
    requestBody,
  }: {
    /**
     * Opaque one-time-disclosed endpoint routing capability.
     */
    token: string,
    /**
     * Stripe v1 timestamped HMAC signature over the exact request body.
     */
    stripeSignature: string,
    requestBody: Record<string, any>,
  }): CancelablePromise<InboundWebhookReceiptResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/hooks/{token}',
      path: {
        'token': token,
      },
      headers: {
        'Stripe-Signature': stripeSignature,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: inbound_webhook_invalid for malformed endpoint configuration/body or a missing Stripe event id; code: inbound_webhook_bad_signature when Stripe-Signature does not verify.`,
        404: `code: not_found`,
        413: `code: inbound_webhook_too_large — the provider request body exceeds 1 MiB.`,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
}
