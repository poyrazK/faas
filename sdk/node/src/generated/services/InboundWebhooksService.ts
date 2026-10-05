/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateInboundWebhookEndpointRequest } from '../models/CreateInboundWebhookEndpointRequest.js';
import type { InboundWebhookEndpointResponse } from '../models/InboundWebhookEndpointResponse.js';
import type { InboundWebhookReceiptResponse } from '../models/InboundWebhookReceiptResponse.js';
import type { PutWebhookAutomationBindingRequest } from '../models/PutWebhookAutomationBindingRequest.js';
import type { UpdateInboundWebhookEndpointRequest } from '../models/UpdateInboundWebhookEndpointRequest.js';
import type { WebhookAutomationBindingResponse } from '../models/WebhookAutomationBindingResponse.js';
import type { WebhookAutomationReceiptResponse } from '../models/WebhookAutomationReceiptResponse.js';
import type { WorkflowCallbackWebhookReceiptResponse } from '../models/WorkflowCallbackWebhookReceiptResponse.js';
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
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
   * Route a verified Stripe endpoint to a published automation.
   * One binding per endpoint, owned by the same app. expected_version is zero
   * for creation and the returned version for updates. take_over_delivery
   * must be true: bound endpoints start automations instead of app delivery.
   * Callback and managed-operation bindings must be removed first.
   * Event types accept edge wildcards; filters use the existing event language.
   *
   * @returns WebhookAutomationBindingResponse Saved binding with its new revision.
   * @throws ApiError
   */
  public static putWebhookAutomationBinding({
    slug,
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Endpoint whose future verified events use this automation binding.
     */
    id: string,
    /**
     * Explicit delivery takeover and published automation selection.
     */
    requestBody: PutWebhookAutomationBindingRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<WebhookAutomationBindingResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/inbound-webhooks/{id}/automation-binding',
      path: {
        'slug': slug,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Plan does not allow workflows.`,
        404: `code: not_found`,
        409: `Binding revision or routing mode conflict (webhook_automation_conflict).`,
        413: `Request body exceeds 64 KiB.`,
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
   * Inspect an endpoint automation binding.
   * Returns the current event selection and revision for this endpoint.
   * @returns WebhookAutomationBindingResponse Current binding.
   * @throws ApiError
   */
  public static getWebhookAutomationBinding({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Endpoint whose future verified events use this automation binding.
     */
    id: string,
  }): CancelablePromise<WebhookAutomationBindingResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/inbound-webhooks/{id}/automation-binding',
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Restore ordinary delivery for future webhook events.
   * Already accepted receipts retain their captured routing decision.
   * @returns void
   * @throws ApiError
   */
  public static deleteWebhookAutomationBinding({
    slug,
    id,
    expectedVersion,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Endpoint whose future verified events use this automation binding.
     */
    id: string,
    /**
     * Current binding revision returned by a previous read or update.
     */
    expectedVersion: number,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/inbound-webhooks/{id}/automation-binding',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'expected_version': expectedVersion,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Binding revision conflict (webhook_automation_conflict).`,
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
   * Inspect a verified provider event and its automation admission.
   * Returns the captured decision and scheduler progress for a retained provider event.
   * @returns WebhookAutomationReceiptResponse Receipt, routing status and admitted run ID when retained.
   * @throws ApiError
   */
  public static getWebhookAutomationReceipt({
    slug,
    id,
    eventId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Endpoint that accepted the provider event being inspected.
     */
    id: string,
    /**
     * Stripe event ID returned in the verified provider receipt.
     */
    eventId: string,
  }): CancelablePromise<WebhookAutomationReceiptResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/inbound-webhooks/{id}/automation-receipts/{event_id}',
      path: {
        'slug': slug,
        'id': id,
        'event_id': eventId,
      },
      errors: {
        401: `code: unauthorized`,
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
   * Verify and durably accept a provider webhook.
   * This route does not use a Gregale bearer key. The opaque URL and the
   * provider signature are the trust boundary. For Stripe, the exact raw
   * body is verified against Stripe-Signature. An exact workflow callback
   * binding completes its callback durably instead of enqueuing an app
   * invocation. Unmatched events keep the ordinary invocation path.
   * Terminal callbacks are acknowledged as ignored after verification.
   * An automation-bound endpoint captures its published definition in durable
   * fanout work. Paused, unpublished or type-unmatched events are durably ignored;
   * content filters are evaluated by the scheduler. Provider retries return the
   * original receipt; changed content for the same event ID returns 409.
   *
   * @returns any Verified and durably accepted, duplicated, or ignored after callback closure.
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
  }): CancelablePromise<(WebhookAutomationReceiptResponse | WorkflowCallbackWebhookReceiptResponse | InboundWebhookReceiptResponse)> {
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
        409: `Changed content for a retained provider event ID (webhook_automation_conflict).`,
        413: `code: inbound_webhook_too_large — the provider request body exceeds 1 MiB.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
}
