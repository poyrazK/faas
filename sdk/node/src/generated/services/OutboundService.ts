/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateOutboundIntegrationRequest } from '../models/CreateOutboundIntegrationRequest.js';
import type { OutboundAppBinding } from '../models/OutboundAppBinding.js';
import type { OutboundAppBindingList } from '../models/OutboundAppBindingList.js';
import type { OutboundIntegrationOffer } from '../models/OutboundIntegrationOffer.js';
import type { OutboundIntegrationOfferList } from '../models/OutboundIntegrationOfferList.js';
import type { OutboundIntegrationUsageResponse } from '../models/OutboundIntegrationUsageResponse.js';
import type { PutOutboundCredentialRequest } from '../models/PutOutboundCredentialRequest.js';
import type { PutOutboundDailyRequestBudgetRequest } from '../models/PutOutboundDailyRequestBudgetRequest.js';
import type { UpdateOutboundBindingPolicyRequest } from '../models/UpdateOutboundBindingPolicyRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OutboundService {
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        409: `The account already has an integration with this name.`,
        429: `The account has reached its customer integration limit.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
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
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
