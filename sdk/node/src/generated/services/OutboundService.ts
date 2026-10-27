/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AIDeploymentUsageList } from '../models/AIDeploymentUsageList.js';
import type { CreatedOutboundAIAccessToken } from '../models/CreatedOutboundAIAccessToken.js';
import type { CreateOutboundAIAccessTokenRequest } from '../models/CreateOutboundAIAccessTokenRequest.js';
import type { CreateOutboundAIEvaluationRequest } from '../models/CreateOutboundAIEvaluationRequest.js';
import type { CreateOutboundAIReplayRequest } from '../models/CreateOutboundAIReplayRequest.js';
import type { CreateOutboundAIRouteRequest } from '../models/CreateOutboundAIRouteRequest.js';
import type { CreateOutboundCredentialKeyRequest } from '../models/CreateOutboundCredentialKeyRequest.js';
import type { CreateOutboundIntegrationRequest } from '../models/CreateOutboundIntegrationRequest.js';
import type { OutboundAIAccessTokenList } from '../models/OutboundAIAccessTokenList.js';
import type { OutboundAIBudgetAlertList } from '../models/OutboundAIBudgetAlertList.js';
import type { OutboundAIContentPolicy } from '../models/OutboundAIContentPolicy.js';
import type { OutboundAIEvaluation } from '../models/OutboundAIEvaluation.js';
import type { OutboundAIEvaluationList } from '../models/OutboundAIEvaluationList.js';
import type { OutboundAIPriceCatalogResponse } from '../models/OutboundAIPriceCatalogResponse.js';
import type { OutboundAIPriceListResponse } from '../models/OutboundAIPriceListResponse.js';
import type { OutboundAIQualityAlertList } from '../models/OutboundAIQualityAlertList.js';
import type { OutboundAIReplay } from '../models/OutboundAIReplay.js';
import type { OutboundAIRequestContent } from '../models/OutboundAIRequestContent.js';
import type { OutboundAIRequestLogList } from '../models/OutboundAIRequestLogList.js';
import type { OutboundAIRoute } from '../models/OutboundAIRoute.js';
import type { OutboundAIRouteCanary } from '../models/OutboundAIRouteCanary.js';
import type { OutboundAIRouteCanaryStatus } from '../models/OutboundAIRouteCanaryStatus.js';
import type { OutboundAIRouteList } from '../models/OutboundAIRouteList.js';
import type { OutboundAIRouteStats } from '../models/OutboundAIRouteStats.js';
import type { OutboundAITagLimits } from '../models/OutboundAITagLimits.js';
import type { OutboundAITagUsageExportRow } from '../models/OutboundAITagUsageExportRow.js';
import type { OutboundAITagUsageResponse } from '../models/OutboundAITagUsageResponse.js';
import type { OutboundAITraceList } from '../models/OutboundAITraceList.js';
import type { OutboundAITraceResponse } from '../models/OutboundAITraceResponse.js';
import type { OutboundAIUsageResponse } from '../models/OutboundAIUsageResponse.js';
import type { OutboundAIUserUsageResponse } from '../models/OutboundAIUserUsageResponse.js';
import type { OutboundAppBinding } from '../models/OutboundAppBinding.js';
import type { OutboundAppBindingList } from '../models/OutboundAppBindingList.js';
import type { OutboundBindingProbePolicy } from '../models/OutboundBindingProbePolicy.js';
import type { OutboundBindingUsageResponse } from '../models/OutboundBindingUsageResponse.js';
import type { OutboundCredentialKey } from '../models/OutboundCredentialKey.js';
import type { OutboundCredentialKeyList } from '../models/OutboundCredentialKeyList.js';
import type { OutboundIntegrationOffer } from '../models/OutboundIntegrationOffer.js';
import type { OutboundIntegrationOfferList } from '../models/OutboundIntegrationOfferList.js';
import type { OutboundIntegrationUsageResponse } from '../models/OutboundIntegrationUsageResponse.js';
import type { Problem } from '../models/Problem.js';
import type { PutOutboundAIFeedbackRequest } from '../models/PutOutboundAIFeedbackRequest.js';
import type { PutOutboundAIPolicyRequest } from '../models/PutOutboundAIPolicyRequest.js';
import type { PutOutboundAIPricesRequest } from '../models/PutOutboundAIPricesRequest.js';
import type { PutOutboundAIRouteCanaryRequest } from '../models/PutOutboundAIRouteCanaryRequest.js';
import type { PutOutboundAIRouteTargetsRequest } from '../models/PutOutboundAIRouteTargetsRequest.js';
import type { PutOutboundAITagLimitsRequest } from '../models/PutOutboundAITagLimitsRequest.js';
import type { PutOutboundBindingAIBudgetRequest } from '../models/PutOutboundBindingAIBudgetRequest.js';
import type { PutOutboundBindingAIGuardrailsRequest } from '../models/PutOutboundBindingAIGuardrailsRequest.js';
import type { PutOutboundBindingDailyRequestBudgetRequest } from '../models/PutOutboundBindingDailyRequestBudgetRequest.js';
import type { PutOutboundCredentialRequest } from '../models/PutOutboundCredentialRequest.js';
import type { PutOutboundDailyRequestBudgetRequest } from '../models/PutOutboundDailyRequestBudgetRequest.js';
import type { PutOutboundRequestPolicyRequest } from '../models/PutOutboundRequestPolicyRequest.js';
import type { PutOutboundRunsBindingRequest } from '../models/PutOutboundRunsBindingRequest.js';
import type { UpdateOutboundBindingPolicyRequest } from '../models/UpdateOutboundBindingPolicyRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OutboundService {
  /**
   * Read an explicitly configured outbound probe policy
   * Read a customer-owned integration's safe-method probe configuration. This does not send traffic to a provider.
   * @returns OutboundBindingProbePolicy Probe policy
   * @returns Problem Probe policy lookup was rejected or the catalog is unavailable
   * @throws ApiError
   */
  public static getOutboundBindingProbePolicy({
    integration,
  }: {
    /**
     * Customer-owned outbound integration identifier.
     */
    integration: string,
  }): CancelablePromise<OutboundBindingProbePolicy | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/probe-policy',
      path: {
        'integration': integration,
      },
    });
  }
  /**
   * Configure an outbound binding probe
   * Declare a GET or HEAD path safe to probe and its expected 2xx status. The path must fit the customer-owned integration's route policy. Configuration sends no provider requests. Requires deploy-write access and MFA.
   * @returns OutboundBindingProbePolicy Confirmed probe policy
   * @returns Problem Probe policy update was rejected or could not be persisted
   * @throws ApiError
   */
  public static setOutboundBindingProbePolicy({
    integration,
    requestBody,
  }: {
    /**
     * Customer-owned outbound integration identifier.
     */
    integration: string,
    requestBody: OutboundBindingProbePolicy,
  }): CancelablePromise<OutboundBindingProbePolicy | Problem> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/probe-policy',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Remove an outbound binding probe configuration
   * Remove a customer-owned integration's probe policy, invalidating prior evidence. Requires deploy-write access and MFA. This does not revoke provider credentials.
   * @returns Problem Probe policy removal was rejected or could not be persisted
   * @throws ApiError
   */
  public static deleteOutboundBindingProbePolicy({
    integration,
  }: {
    /**
     * Customer-owned outbound integration identifier.
     */
    integration: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/outbound/integrations/{integration}/probe-policy',
      path: {
        'integration': integration,
      },
    });
  }
  /**
   * List managed outbound integrations available to this account.
   * Provider credentials and gateway tokens are never returned.
   * @returns OutboundIntegrationOfferList Available managed integrations.
   * @throws ApiError
   */
  public static listOutboundIntegrationOffers(): CancelablePromise<OutboundIntegrationOfferList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations',
      errors: {
        401: `code: unauthorized`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Create a customer-owned managed outbound integration.
   * Requires MFA and deploy-write scope. The origin must resolve only to globally reachable addresses. Provider Authorization is uploaded separately and sealed at rest.
   * @returns OutboundIntegrationOffer Customer integration created without credential material.
   * @throws ApiError
   */
  public static createOutboundIntegration({
    requestBody,
  }: {
    requestBody: CreateOutboundIntegrationRequest,
  }): CancelablePromise<OutboundIntegrationOffer> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/outbound/integrations',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        409: `The account already has an integration with this name.`,
        429: `The account has reached its customer integration limit.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Set or rotate a customer-held outbound provider credential.
   * The Authorization value is sealed at rest and never returned. Requires MFA and deploy-write scope.
   * @returns void
   * @throws ApiError
   */
  public static putOutboundCredential({
    integration,
    requestBody,
  }: {
    /**
     * UUID of an account-owned customer-sealed integration.
     */
    integration: string,
    requestBody: PutOutboundCredentialRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/credential',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Revoke a customer-held outbound provider credential.
   * Idempotent for an existing integration; subsequent gateway calls fail closed.
   * @returns void
   * @throws ApiError
   */
  public static deleteOutboundCredential({
    integration,
  }: {
    /**
     * UUID of an account-owned customer-sealed integration.
     */
    integration: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/outbound/integrations/{integration}/credential',
      path: {
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Set or clear a customer-owned integration's daily request limit.
   * Requires MFA and deploy-write scope. The configured value cannot exceed the account plan's per-integration ceiling. Set daily_request_limit to null to clear the customer-selected limit.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundIntegrationDailyBudget({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the integration whose daily budget is being changed.
     */
    integration: string,
    requestBody: PutOutboundDailyRequestBudgetRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/budget',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace a customer-owned integration's rate, burst, concurrency, and timeout policy.
   * Requires MFA and deploy-write scope. Every value must fit the account plan ceiling. Changes are enforced on subsequent admissions without an outbound gateway restart; already-admitted calls keep their existing deadline.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundIntegrationRequestPolicy({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the customer-owned integration whose request policy is being changed.
     */
    integration: string,
    requestBody: PutOutboundRequestPolicyRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/request-policy',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Set or clear a customer-owned integration's AI provider and daily token limit.
   * Requires MFA and deploy-write scope. The gateway extracts token usage from responses in the selected provider's format and stops new calls once the UTC day's settled input plus output tokens reach the limit; calls already in flight may finish above it.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundIntegrationAiPolicy({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the customer-owned integration whose AI gateway settings are being changed.
     */
    integration: string,
    requestBody: PutOutboundAIPolicyRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/ai',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one UTC day of AI gateway token usage by caller and model.
   * Requires MFA and read scope. Rollups are kept for 90 days.
   * @returns OutboundAIUsageResponse AI usage for the selected day.
   * @throws ApiError
   */
  public static getOutboundIntegrationAiUsage({
    integration,
    date,
  }: {
    /**
     * UUID of the integration whose AI usage is being read.
     */
    integration: string,
    /**
     * UTC day as YYYY-MM-DD within the last 90 days; defaults to today.
     */
    date?: string,
  }): CancelablePromise<OutboundAIUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-usage',
      path: {
        'integration': integration,
      },
      query: {
        'date': date,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one UTC day of AI gateway usage by end user.
   * Requires MFA and read scope. End users are the X-Gregale-AI-User values apps send; rows are kept for 31 days and listed by cost, then tokens.
   * @returns OutboundAIUserUsageResponse Per-end-user AI usage for the selected day.
   * @throws ApiError
   */
  public static getOutboundIntegrationAiUserUsage({
    integration,
    date,
    endUser,
    limit,
  }: {
    /**
     * UUID of the integration whose per-end-user AI usage is being read.
     */
    integration: string,
    /**
     * UTC day as YYYY-MM-DD within the last 31 days; defaults to today.
     */
    date?: string,
    /**
     * Read only this end user's row ("~other" reads the overflow row).
     */
    endUser?: string,
    /**
     * Rows to return, 1 to 1000; defaults to 1000.
     */
    limit?: number,
  }): CancelablePromise<OutboundAIUserUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-usage/users',
      path: {
        'integration': integration,
      },
      query: {
        'date': date,
        'end_user': endUser,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Export per-end-user AI gateway usage for a range of UTC days.
   * Requires MFA and read scope. Streams every per-end-user row of the days from through to, ordered by day and end user. CSV columns are usage_date, end_user, requests, error_requests, input_tokens, output_tokens, tokens and cost_millicents; NDJSON lines carry the same fields.
   * @returns binary Per-end-user usage rows.
   * @throws ApiError
   */
  public static exportOutboundIntegrationAiUserUsage({
    integration,
    from,
    to,
    format = 'ndjson',
  }: {
    /**
     * UUID of the integration whose per-end-user AI usage is exported.
     */
    integration: string,
    /**
     * First UTC day as YYYY-MM-DD within the last 31 days; defaults to today.
     */
    from?: string,
    /**
     * Last UTC day as YYYY-MM-DD, not before from; defaults to today.
     */
    to?: string,
    /**
     * NDJSON lines (the default) or CSV rows with a header.
     */
    format?: 'ndjson' | 'csv',
  }): CancelablePromise<Blob> {
    return __request(OpenAPI, {
      responseType: 'blob',
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-usage/users/export',
      path: {
        'integration': integration,
      },
      query: {
        'from': from,
        'to': to,
        'format': format,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read the built-in AI price catalog of one provider.
   * Requires MFA and read scope. The catalog prices the models an integration's own price list does not name. Prices are the providers' list prices in US dollars, rounded up.
   * @returns OutboundAIPriceCatalogResponse The provider's catalog, or the one entry that prices the model.
   * @throws ApiError
   */
  public static getOutboundAiPriceCatalog({
    provider,
    model,
  }: {
    /**
     * AI provider whose catalog is listed.
     */
    provider: 'openai' | 'anthropic' | 'gemini' | 'azure_openai' | 'bedrock' | 'vertex',
    /**
     * Return only the catalog entry that would price this model.
     */
    model?: string,
  }): CancelablePromise<OutboundAIPriceCatalogResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-price-catalog',
      query: {
        'provider': provider,
        'model': model,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
      },
    });
  }
  /**
   * Read the AI price list in effect.
   * Requires MFA and read scope. Returns the prices the integration's calls are costed at.
   * @returns OutboundAIPriceListResponse The price list in effect.
   * @throws ApiError
   */
  public static getOutboundIntegrationAiPrices({
    integration,
  }: {
    /**
     * UUID of the AI integration whose price list is read or replaced.
     */
    integration: string,
  }): CancelablePromise<OutboundAIPriceListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-prices',
      path: {
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace a customer AI integration's price list.
   * Requires MFA and deploy-write scope. The new list prices calls settled from now on; earlier lists stay for costs already recorded.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundIntegrationAiPrices({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the AI integration whose price list is read or replaced.
     */
    integration: string,
    requestBody: PutOutboundAIPricesRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/ai-prices',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List an integration's extra provider keys.
   * Requires MFA and read scope. Values are write-only and never returned.
   * @returns OutboundCredentialKeyList Extra keys, oldest first.
   * @throws ApiError
   */
  public static listOutboundCredentialKeys({
    integration,
  }: {
    /**
     * UUID of the integration whose extra provider keys are listed or added.
     */
    integration: string,
  }): CancelablePromise<OutboundCredentialKeyList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/credential-keys',
      path: {
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Add an extra provider key to a customer-sealed integration.
   * Requires MFA and deploy-write scope. The outbound gateway picks keys round-robin and moves a call to the next key when one returns 429. At most 8 extra keys.
   * @returns OutboundCredentialKey Key sealed and stored.
   * @throws ApiError
   */
  public static createOutboundCredentialKey({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the integration whose extra provider keys are listed or added.
     */
    integration: string,
    requestBody: CreateOutboundCredentialKeyRequest,
  }): CancelablePromise<OutboundCredentialKey> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/outbound/integrations/{integration}/credential-keys',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        409: `A key with this label already exists.`,
        429: `The integration has reached its extra key limit.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Remove an extra provider key.
   * Requires MFA and deploy-write scope. The outbound gateway stops using it on its next call.
   * @returns void
   * @throws ApiError
   */
  public static deleteOutboundCredentialKey({
    integration,
    key,
  }: {
    /**
     * UUID of the integration that holds the extra key.
     */
    integration: string,
    /**
     * UUID of the extra key.
     */
    key: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/outbound/integrations/{integration}/credential-keys/{key}',
      path: {
        'integration': integration,
        'key': key,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one UTC day of AI gateway usage by tag.
   * Requires MFA and read scope. Tags are the key=value pairs apps send in X-Gregale-AI-Tags; rows are kept for 31 days and listed by cost, then tokens, with each tag's daily cost limit.
   * @returns OutboundAITagUsageResponse AI usage by tag for the selected day.
   * @throws ApiError
   */
  public static getOutboundAiTagUsage({
    integration,
    date,
    key,
    limit,
  }: {
    /**
     * UUID of the integration whose AI usage by tag is being read.
     */
    integration: string,
    /**
     * UTC day of the tag rows as YYYY-MM-DD within the last 31 days; defaults to today.
     */
    date?: string,
    /**
     * Read only this tag key's tags, its overflow row included.
     */
    key?: string,
    /**
     * Tag rows to return, 1 to 1000; defaults to 1000.
     */
    limit?: number,
  }): CancelablePromise<OutboundAITagUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-usage/tags',
      path: {
        'integration': integration,
      },
      query: {
        'date': date,
        'key': key,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Export a day range of AI gateway usage by tag.
   * Requires MFA and read scope. Streams every tag row of the UTC days from through to, optionally of one key, ordered by day and tag, as NDJSON (the default) or CSV with the columns of OutboundAITagUsageExportRow.
   * @returns OutboundAITagUsageExportRow The tag rows of the range.
   * @throws ApiError
   */
  public static exportOutboundAiTagUsage({
    integration,
    from,
    to,
    key,
    format,
  }: {
    /**
     * UUID of the integration whose AI usage by tag is exported.
     */
    integration: string,
    /**
     * First UTC day of the export as YYYY-MM-DD within the last 31 days; defaults to today.
     */
    from?: string,
    /**
     * Last UTC day of the export as YYYY-MM-DD, not before from; defaults to today.
     */
    to?: string,
    /**
     * Export only this tag key's rows, its overflow row included.
     */
    key?: string,
    /**
     * ndjson (the default) or csv.
     */
    format?: 'ndjson' | 'csv',
  }): CancelablePromise<OutboundAITagUsageExportRow> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-usage/tags/export',
      path: {
        'integration': integration,
      },
      query: {
        'from': from,
        'to': to,
        'key': key,
        'format': format,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an AI integration's per-tag daily cost limits.
   * Requires MFA and read scope. Returns each tag's daily cost limit.
   * @returns OutboundAITagLimits The integration's tag limits, ordered by tag.
   * @throws ApiError
   */
  public static getOutboundAiTagLimits({
    integration,
  }: {
    /**
     * UUID of the managed AI integration.
     */
    integration: string,
  }): CancelablePromise<OutboundAITagLimits> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-tag-limits',
      path: {
        'integration': integration,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace an AI integration's per-tag daily cost limits.
   * Requires MFA and deploy-write scope. A call carrying a limited tag is refused with 429 outbound_budget_exhausted (X-Gregale-Outbound-Rejection tag_daily_cost_limit) when the tag's settled UTC-day cost, live reservations and the call's reservation would pass the limit. An empty list removes every limit.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundAiTagLimits({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the managed AI integration.
     */
    integration: string,
    requestBody: PutOutboundAITagLimitsRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/ai-tag-limits',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List AI budget alerts recorded at 80% and 100%.
   * Requires MFA and read scope. Newest first; kept for 90 days.
   * @returns OutboundAIBudgetAlertList Budget alerts.
   * @throws ApiError
   */
  public static listOutboundAiBudgetAlerts({
    integration,
    limit,
  }: {
    /**
     * UUID of the integration whose AI budget alerts are listed.
     */
    integration: string,
    /**
     * Page size, 1 to 100 (default 100).
     */
    limit?: number,
  }): CancelablePromise<OutboundAIBudgetAlertList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-alerts',
      path: {
        'integration': integration,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Page the opt-in AI request log.
   * Requires MFA and read scope. Newest first; rows are kept for 7 days and hold metadata. content_captured marks calls whose bodies were captured.
   * @returns OutboundAIRequestLogList One page of logged calls.
   * @throws ApiError
   */
  public static listOutboundAiRequestLog({
    integration,
    limit,
    before,
  }: {
    /**
     * UUID of the integration whose AI request log is read.
     */
    integration: string,
    /**
     * Page size, 1 to 200 (default 200).
     */
    limit?: number,
    /**
     * The previous page's next_before.
     */
    before?: string,
  }): CancelablePromise<OutboundAIRequestLogList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-requests',
      path: {
        'integration': integration,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Page an integration's AI traces.
   * Requires MFA and read scope. A trace groups the calls of one agent task, named by X-Gregale-AI-Trace or the trace ID of a W3C traceparent header. Traces are recorded while the integration keeps a request log or has trace limits, and are kept for 7 days. Newest last call first.
   * @returns OutboundAITraceList One page of traces.
   * @throws ApiError
   */
  public static listOutboundAiTraces({
    integration,
    limit,
    before,
    appId,
    endUser,
  }: {
    /**
     * UUID of the integration whose AI traces are read.
     */
    integration: string,
    /**
     * Traces per page, 1 to 200 (default 200).
     */
    limit?: number,
    /**
     * The previous page's next_before, to read traces last called earlier.
     */
    before?: string,
    /**
     * Only traces whose first call came from this app.
     */
    appId?: string,
    /**
     * Only traces whose first call named this X-Gregale-AI-User.
     */
    endUser?: string,
  }): CancelablePromise<OutboundAITraceList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-traces',
      path: {
        'integration': integration,
      },
      query: {
        'limit': limit,
        'before': before,
        'app_id': appId,
        'end_user': endUser,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an AI trace with its logged calls.
   * Requires MFA and read scope. Calls are the trace's request-log entries, oldest first, at most 500; calls_truncated marks a longer trace.
   * @returns OutboundAITraceResponse The trace.
   * @throws ApiError
   */
  public static getOutboundAiTrace({
    integration,
    trace,
  }: {
    /**
     * UUID of the integration that recorded the trace.
     */
    integration: string,
    /**
     * The trace ID, path-escaped.
     */
    trace: string,
  }): CancelablePromise<OutboundAITraceResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-traces/{trace}',
      path: {
        'integration': integration,
        'trace': trace,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List an integration's AI quality drift alerts.
   * Requires MFA and read scope. Alerts are newest first and kept for 31 days; each fires once per model, metric and UTC day.
   * @returns OutboundAIQualityAlertList The alerts.
   * @throws ApiError
   */
  public static listOutboundAiQualityAlerts({
    integration,
    limit,
  }: {
    /**
     * UUID of the integration whose alerts are listed.
     */
    integration: string,
    /**
     * Alerts to return, 1 to 100 (default 50).
     */
    limit?: number,
  }): CancelablePromise<OutboundAIQualityAlertList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-quality-alerts',
      path: {
        'integration': integration,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Rate a logged AI call.
   * Requires MFA and deploy-write scope. Apps rate their own calls through the gateway at /i/{integration}/_gregale/feedback; this rates any logged call on a person's behalf. A later rating replaces the earlier one, and the call's daily usage counts the change.
   * @returns void
   * @throws ApiError
   */
  public static putOutboundAiRequestFeedback({
    integration,
    request,
    requestBody,
  }: {
    /**
     * UUID of the integration that logged the call.
     */
    integration: string,
    /**
     * ID of the request-log entry, the value X-Gregale-AI-Call returned.
     */
    request: string,
    requestBody: PutOutboundAIFeedbackRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/ai-requests/{request}/feedback',
      path: {
        'integration': integration,
        'request': request,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replay a captured AI call on other integrations or models.
   * Requires MFA and deploy-write scope. The outbound gateway resends the captured request to each target without streaming; the calls count against the targets' budgets. At most 100 replays per integration per UTC day.
   * @returns OutboundAIReplay The pending replay.
   * @throws ApiError
   */
  public static createOutboundAiReplay({
    integration,
    request,
    requestBody,
  }: {
    /**
     * UUID of the integration whose captured call is replayed.
     */
    integration: string,
    /**
     * ID of the request-log entry whose capture is replayed.
     */
    request: string,
    requestBody: CreateOutboundAIReplayRequest,
  }): CancelablePromise<OutboundAIReplay> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/outbound/integrations/{integration}/ai-requests/{request}/replays',
      path: {
        'integration': integration,
        'request': request,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        409: `The call has no capture that can be resent.`,
        429: `The integration reached its replays for the UTC day.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an AI replay and its results.
   * Requires MFA and read scope. Replays are kept for 3 days.
   * @returns OutboundAIReplay The replay.
   * @throws ApiError
   */
  public static getOutboundAiReplay({
    replay,
  }: {
    /**
     * UUID of the replay.
     */
    replay: string,
  }): CancelablePromise<OutboundAIReplay> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-replays/{replay}',
      path: {
        'replay': replay,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace an AI integration's content guardrails.
   * Requires MFA and deploy-write scope. An active policy requires an AI provider; with one, request bodies the gateway cannot read are refused.
   * @returns void
   * @throws ApiError
   */
  public static putOutboundAiContentPolicy({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the customer AI integration.
     */
    integration: string,
    requestBody: OutboundAIContentPolicy,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/ai-content-policy',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one logged AI call's captured bodies.
   * Requires MFA and read scope. Returns the start of the request and response bodies the gateway sent and received when request_log_content was on. Captures are kept for 3 days; a call without one is not found.
   * @returns OutboundAIRequestContent The captured bodies.
   * @throws ApiError
   */
  public static getOutboundAiRequestContent({
    integration,
    request,
  }: {
    /**
     * UUID of the integration whose logged call is rated.
     */
    integration: string,
    /**
     * ID of the request-log entry.
     */
    request: string,
  }): CancelablePromise<OutboundAIRequestContent> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/ai-requests/{request}/content',
      path: {
        'integration': integration,
        'request': request,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List the account's AI fallback routes.
   * Requires MFA and read scope. Lists every AI route the account owns.
   * @returns OutboundAIRouteList AI routes.
   * @throws ApiError
   */
  public static listOutboundAiRoutes(): CancelablePromise<OutboundAIRouteList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-routes',
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Create an AI fallback route over managed AI integrations.
   * Requires MFA and deploy-write scope. Every target must be an enabled managed AI integration of this account; a native route needs one ai_provider, an openai route translates OpenAI chat completions for anthropic and gemini targets. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider path}; the gateway tries targets in order and moves on after a budget or availability rejection, a provider 408, 429 or 5xx, or a transport error, before any response byte is sent.
   * @returns OutboundAIRoute Route created.
   * @throws ApiError
   */
  public static createOutboundAiRoute({
    requestBody,
  }: {
    requestBody: CreateOutboundAIRouteRequest,
  }): CancelablePromise<OutboundAIRoute> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/outbound/ai-routes',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        409: `A route with this name already exists.`,
        429: `The account has reached its AI route limit.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one AI fallback route.
   * Requires MFA and read scope.
   * @returns OutboundAIRoute The route.
   * @throws ApiError
   */
  public static getOutboundAiRoute({
    route,
  }: {
    /**
     * UUID of the AI route.
     */
    route: string,
  }): CancelablePromise<OutboundAIRoute> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-routes/{route}',
      path: {
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Delete an AI fallback route.
   * Requires MFA and deploy-write scope.
   * @returns void
   * @throws ApiError
   */
  public static deleteOutboundAiRoute({
    route,
  }: {
    /**
     * UUID of the AI route.
     */
    route: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/outbound/ai-routes/{route}',
      path: {
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace an AI route's ordered targets.
   * Requires MFA and deploy-write scope. The same ownership and route-format rules as creation apply.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundAiRouteTargets({
    route,
    requestBody,
  }: {
    /**
     * UUID of the AI route whose targets are replaced.
     */
    route: string,
    requestBody: PutOutboundAIRouteTargetsRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/ai-routes/{route}/targets',
      path: {
        'route': route,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an AI route's model canary with both targets' counts.
   * Requires MFA and read scope. Returns the canary with the route's first target's and the canary target's settled attempts over the canary's window. A route without a canary is not found.
   * @returns OutboundAIRouteCanaryStatus The canary and its counts.
   * @throws ApiError
   */
  public static getOutboundAiRouteCanary({
    route,
  }: {
    /**
     * UUID of the AI route whose model canary is read or changed.
     */
    route: string,
  }): CancelablePromise<OutboundAIRouteCanaryStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-routes/{route}/canary',
      path: {
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Start or replace an AI route's model canary.
   * Requires MFA and deploy-write scope. The canary target receives the given share of the route's calls first and is rolled back by the outbound gateway when its error rate or time to first byte is worse than the first target's by more than the margins. Replacing a canary clears its counts.
   * @returns OutboundAIRouteCanary The canary, active.
   * @throws ApiError
   */
  public static setOutboundAiRouteCanary({
    route,
    requestBody,
  }: {
    /**
     * UUID of the AI route whose model canary is read or changed.
     */
    route: string,
    requestBody: PutOutboundAIRouteCanaryRequest,
  }): CancelablePromise<OutboundAIRouteCanary> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/ai-routes/{route}/canary',
      path: {
        'route': route,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Stop an AI route's model canary.
   * Requires MFA and deploy-write scope. The route keeps its targets in order.
   * @returns void
   * @throws ApiError
   */
  public static deleteOutboundAiRouteCanary({
    route,
  }: {
    /**
     * UUID of the AI route whose model canary is read or changed.
     */
    route: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/outbound/ai-routes/{route}/canary',
      path: {
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an AI route's targets' recent attempts.
   * Requires MFA and read scope. Returns each target's settled attempts, errors, mean time to first byte and mean cost per successful attempt over the window latency and cost routes judge. Attempts are counted for latency and cost routes and during a canary; other routes report zeros.
   * @returns OutboundAIRouteStats The route's targets' attempts.
   * @throws ApiError
   */
  public static getOutboundAiRouteStats({
    route,
  }: {
    /**
     * UUID of the AI route whose targets' recent attempts are read.
     */
    route: string,
  }): CancelablePromise<OutboundAIRouteStats> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-routes/{route}/stats',
      path: {
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Promote an AI route's model canary.
   * Requires MFA and deploy-write scope. Moves the canary target to the front of the route, keeps the other targets in order, and ends the canary.
   * @returns OutboundAIRoute The route with its new order.
   * @throws ApiError
   */
  public static promoteOutboundAiRouteCanary({
    route,
  }: {
    /**
     * UUID of the AI route whose canary target becomes its first target.
     */
    route: string,
  }): CancelablePromise<OutboundAIRoute> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/outbound/ai-routes/{route}/canary/promote',
      path: {
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List an AI route's latest evaluations.
   * Requires MFA and read scope. Newest first, at most 100, without their samples.
   * @returns OutboundAIEvaluationList The route's evaluations.
   * @throws ApiError
   */
  public static listOutboundAiEvaluations({
    route,
  }: {
    /**
     * UUID of the AI route whose target is evaluated.
     */
    route: string,
  }): CancelablePromise<OutboundAIEvaluationList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-routes/{route}/evaluations',
      path: {
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Evaluate an AI route target before a canary.
   * Requires MFA and deploy-write scope. The outbound gateway replays the route's first target's latest captured OpenAI chat completions and Responses calls on the target and asks the judge model which answer is better, in a random order. The evaluation passes when losses and candidate errors are at most max_loss_rate percent of the sample; a passing evaluation starts its canary, if it has one, unless the route already has an active canary or the target changed. Candidate and judge calls are metered and capped under the caller evaluation. The first target's integration needs request_log_content.
   * @returns OutboundAIEvaluation The pending evaluation.
   * @throws ApiError
   */
  public static createOutboundAiEvaluation({
    route,
    requestBody,
  }: {
    /**
     * UUID of the AI route whose target is evaluated.
     */
    route: string,
    requestBody: CreateOutboundAIEvaluationRequest,
  }): CancelablePromise<OutboundAIEvaluation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/outbound/ai-routes/{route}/evaluations',
      path: {
        'route': route,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        429: `The route reached its evaluations for the UTC day.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an AI evaluation and its samples.
   * Requires MFA and read scope. Evaluations are kept for 30 days.
   * @returns OutboundAIEvaluation The evaluation.
   * @throws ApiError
   */
  public static getOutboundAiEvaluation({
    evaluation,
  }: {
    /**
     * UUID of the evaluation.
     */
    evaluation: string,
  }): CancelablePromise<OutboundAIEvaluation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/ai-evaluations/{evaluation}',
      path: {
        'evaluation': evaluation,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Grant or revoke use of a managed integration by stateless Runs.
   * Requires MFA and deploy-write scope. Grants are separate from app bindings and require an enabled integration with a configured managed credential and at least one allowed route. Run tool calls are available only after the Runs broker is enabled for the deployment.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundIntegrationRunsBinding({
    integration,
    requestBody,
  }: {
    /**
     * UUID of the account-owned integration whose Runs access is changing.
     */
    integration: string,
    requestBody: PutOutboundRunsBindingRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/outbound/integrations/{integration}/runs',
      path: {
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Integration is missing a managed credential or allowed route.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read the integration's current UTC-day outbound request usage.
   * Requires MFA and read-surface scope. Counts requests admitted by the gateway, including provider calls that later fail. Usage resets at the returned UTC midnight timestamp.
   * @returns OutboundIntegrationUsageResponse Current UTC-day request count and configured limit.
   * @throws ApiError
   */
  public static getOutboundIntegrationUsage({
    integration,
  }: {
    /**
     * UUID of the integration whose current usage is being read.
     */
    integration: string,
  }): CancelablePromise<OutboundIntegrationUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/outbound/integrations/{integration}/usage',
      path: {
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Delete a customer-owned managed outbound integration.
   * Requires MFA and deploy-write scope. Deletes the integration, its sealed credential, app bindings, and admission state. Operator-provisioned integrations cannot be deleted here.
   * @returns void
   * @throws ApiError
   */
  public static deleteOutboundIntegration({
    integration,
  }: {
    /**
     * UUID of a customer-owned managed integration.
     */
    integration: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/outbound/integrations/{integration}',
      path: {
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List an app's AI gateway calls by deployment.
   * Requires MFA and read scope. Each deployment with AI gateway calls in the last 14 days, latest call first, at most 50: its calls, provider errors, flagged answers, cost, tokens and mean time to first byte. Only calls made from the app's instances count; access tokens, Runs, replays and cached responses do not.
   * @returns AIDeploymentUsageList The app's deployments.
   * @throws ApiError
   */
  public static listAiDeployments({
    slug,
  }: {
    /**
     * The app whose deployments to list.
     */
    slug: string,
  }): CancelablePromise<AIDeploymentUsageList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/ai-deployments',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List an app's AI gateway access tokens.
   * Requires MFA and read scope. Newest first, without their secrets.
   * @returns OutboundAIAccessTokenList The app's tokens.
   * @throws ApiError
   */
  public static listOutboundAiAccessTokens({
    slug,
  }: {
    /**
     * The app the tokens act as.
     */
    slug: string,
  }): CancelablePromise<OutboundAIAccessTokenList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/ai-access-tokens',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Issue an AI gateway access token for an app.
   * Requires MFA and deploy-write scope. A caller outside Gregale, such as local development, CI or a notebook, sets its SDK's base URL to the AI gateway's public host and puts the token where the SDK puts its API key (Authorization Bearer, x-api-key, x-goog-api-key or api-key). Its calls are the app's: the app's bindings, guardrails, caps and content policy apply. The secret is returned only in this response. An app holds at most 20 live tokens.
   * @returns CreatedOutboundAIAccessToken The token and its secret.
   * @throws ApiError
   */
  public static createOutboundAiAccessToken({
    slug,
    requestBody,
  }: {
    /**
     * The app the tokens act as.
     */
    slug: string,
    requestBody: CreateOutboundAIAccessTokenRequest,
  }): CancelablePromise<CreatedOutboundAIAccessToken> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/ai-access-tokens',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        409: `The app already has a live token with this name, or holds its most live tokens.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Revoke an AI gateway access token.
   * Requires MFA and deploy-write scope. The outbound gateway may accept the token for up to 30 seconds more.
   * @returns void
   * @throws ApiError
   */
  public static revokeOutboundAiAccessToken({
    slug,
    token,
  }: {
    /**
     * The app the token acts as.
     */
    slug: string,
    /**
     * UUID of the access token.
     */
    token: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/ai-access-tokens/{token}',
      path: {
        'slug': slug,
        'token': token,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List an app's managed outbound bindings.
   * @returns OutboundAppBindingList Current app bindings, without credential material.
   * @throws ApiError
   */
  public static listOutboundAppBindings({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<OutboundAppBindingList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/outbound-bindings',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Bind an app to an existing managed outbound integration.
   * Idempotent. The integration is operator-provisioned; this request never carries a provider key.
   * @returns OutboundAppBinding Binding stored; a customer-sealed integration also needs a configured credential to serve traffic.
   * @throws ApiError
   */
  public static bindOutboundIntegration({
    slug,
    integration,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of an account-owned managed integration.
     */
    integration: string,
  }): CancelablePromise<OutboundAppBinding> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/outbound-bindings/{integration}',
      path: {
        'slug': slug,
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Narrow an app's managed outbound route policy.
   * Methods and paths must remain within the operator-approved integration policy. Requires MFA and deploy-write scope.
   * @returns void
   * @throws ApiError
   */
  public static updateOutboundBindingPolicy({
    slug,
    integration,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of an account-owned managed integration.
     */
    integration: string,
    requestBody: UpdateOutboundBindingPolicyRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/outbound-bindings/{integration}',
      path: {
        'slug': slug,
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Remove an app's managed outbound binding.
   * Idempotent; returns 204 even when the binding is already absent.
   * @returns void
   * @throws ApiError
   */
  public static unbindOutboundIntegration({
    slug,
    integration,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of an account-owned managed integration.
     */
    integration: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/outbound-bindings/{integration}',
      path: {
        'slug': slug,
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Set or clear an app binding's daily outbound request limit.
   * Requires MFA and deploy-write scope. The configured limit cannot exceed the account plan ceiling. Send null to clear the app-specific limit.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundBindingDailyBudget({
    slug,
    integration,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the app's attached integration whose limit is being configured.
     */
    integration: string,
    requestBody: PutOutboundBindingDailyRequestBudgetRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/outbound-bindings/{integration}/budget',
      path: {
        'slug': slug,
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Set or clear an app's own daily AI token limit on an AI integration.
   * Requires MFA and deploy-write scope. Admission stops the app's new calls once its settled input plus output tokens reach the limit; other apps are unaffected.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundBindingAiBudget({
    slug,
    integration,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the app's bound AI integration.
     */
    integration: string,
    requestBody: PutOutboundBindingAIBudgetRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/outbound-bindings/{integration}/ai-budget',
      path: {
        'slug': slug,
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace an app's AI guardrails on an AI integration.
   * Requires MFA and deploy-write scope. Sets per-minute request and token limits, a model allow-list and a per-request output-token ceiling; the gateway adds the ceiling to requests that name none.
   * @returns void
   * @throws ApiError
   */
  public static setOutboundBindingAiGuardrails({
    slug,
    integration,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the app's bound AI integration whose guardrails are replaced.
     */
    integration: string,
    requestBody: PutOutboundBindingAIGuardrailsRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/outbound-bindings/{integration}/ai-guardrails',
      path: {
        'slug': slug,
        'integration': integration,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_feature_gated — the account plan does not include the AI gateway; upgrade to Hobby or above.`,
        403: `code: outbound_ai_gateway_unavailable — the AI gateway is closed on this platform, or the account is not yet in its staged preview rollout.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an app binding's current UTC-day outbound request usage.
   * Requires MFA and read-surface scope. Reports UTC-day admissions for this app-to-integration binding, including provider calls that later fail; the returned timestamp marks its reset.
   * @returns OutboundBindingUsageResponse Current UTC-day request count and effective binding limit.
   * @throws ApiError
   */
  public static getOutboundBindingUsage({
    slug,
    integration,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the app's attached integration whose usage is requested.
     */
    integration: string,
  }): CancelablePromise<OutboundBindingUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/outbound-bindings/{integration}/usage',
      path: {
        'slug': slug,
        'integration': integration,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
