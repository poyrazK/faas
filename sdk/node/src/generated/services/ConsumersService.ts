/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerListResponse } from '../models/APIConsumerListResponse.js';
import type { APIConsumerPlanAssignmentListResponse } from '../models/APIConsumerPlanAssignmentListResponse.js';
import type { APIConsumerPlanAssignmentResponse } from '../models/APIConsumerPlanAssignmentResponse.js';
import type { APIConsumerPlanListResponse } from '../models/APIConsumerPlanListResponse.js';
import type { APIConsumerPlanResponse } from '../models/APIConsumerPlanResponse.js';
import type { APIConsumerRateCardListResponse } from '../models/APIConsumerRateCardListResponse.js';
import type { APIConsumerRateCardResponse } from '../models/APIConsumerRateCardResponse.js';
import type { APIConsumerResponse } from '../models/APIConsumerResponse.js';
import type { APIConsumerUsageCompletenessResponse } from '../models/APIConsumerUsageCompletenessResponse.js';
import type { APIConsumerUsageQuoteResponse } from '../models/APIConsumerUsageQuoteResponse.js';
import type { APIConsumerUsageResponse } from '../models/APIConsumerUsageResponse.js';
import type { APIConsumerUsageStatementHandoffResponse } from '../models/APIConsumerUsageStatementHandoffResponse.js';
import type { APIConsumerUsageStatementListResponse } from '../models/APIConsumerUsageStatementListResponse.js';
import type { APIConsumerUsageStatementResponse } from '../models/APIConsumerUsageStatementResponse.js';
import type { AssignAPIConsumerPlanRequest } from '../models/AssignAPIConsumerPlanRequest.js';
import type { ClaimAPIConsumerUsageStatementRequest } from '../models/ClaimAPIConsumerUsageStatementRequest.js';
import type { ConsumerKeyListResponse } from '../models/ConsumerKeyListResponse.js';
import type { ConsumerKeyResponse } from '../models/ConsumerKeyResponse.js';
import type { CreateAPIConsumerPlanRequest } from '../models/CreateAPIConsumerPlanRequest.js';
import type { CreateAPIConsumerRateCardRequest } from '../models/CreateAPIConsumerRateCardRequest.js';
import type { CreateAPIConsumerRequest } from '../models/CreateAPIConsumerRequest.js';
import type { CreateAPIConsumerUsageStatementRequest } from '../models/CreateAPIConsumerUsageStatementRequest.js';
import type { CreateConsumerKeyRequest } from '../models/CreateConsumerKeyRequest.js';
import type { UpdateAPIConsumerPlanLimitsRequest } from '../models/UpdateAPIConsumerPlanLimitsRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ConsumersService {
  /**
   * List stable API consumer identities for an app.
   * @returns APIConsumerListResponse Consumer identities (never includes credentials).
   * @throws ApiError
   */
  public static listApiConsumers({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<APIConsumerListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Register a stable API consumer identity.
   * @returns APIConsumerResponse The created consumer identity.
   * @throws ApiError
   */
  public static createApiConsumer({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAPIConsumerRequest,
  }): CancelablePromise<APIConsumerResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Fetch one API consumer identity.
   * @returns APIConsumerResponse The consumer identity.
   * @throws ApiError
   */
  public static getApiConsumer({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Target API consumer identity UUID.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Revoke an API consumer identity.
   * @returns APIConsumerResponse The revoked consumer identity.
   * @throws ApiError
   */
  public static revokeApiConsumer({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Target API consumer identity UUID.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumers/{consumer_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read durable minute usage for one API consumer.
   * Returns idempotent request, error, and billable-unit counters for the
   * selected stable consumer. The ledger is independent from sampled
   * request telemetry. Anonymous traffic is retained separately and is
   * not charged to this consumer identity.
   *
   * @returns APIConsumerUsageResponse Durable API consumer usage over the requested window.
   * @throws ApiError
   */
  public static getApiConsumerUsage({
    slug,
    consumerId,
    since,
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose durable minute usage is returned.
     */
    consumerId: string,
    /**
     * RFC3339 lower bound. Defaults to the trailing 30 days.
     */
    since?: string,
    /**
     * RFC3339 exclusive upper bound. Defaults to UTC midnight today.
     */
    until?: string,
  }): CancelablePromise<APIConsumerUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Quote one API consumer's priced usage.
   * Applies the app's immutable, versioned request rate cards to durable
   * consumer usage. The result is a deterministic estimate, not an invoice
   * or a payment authorization. Usage before the first effective rate card
   * is returned as unpriced_units rather than silently treated as free.
   *
   * @returns APIConsumerUsageQuoteResponse Deterministic usage quote over the requested window.
   * @throws ApiError
   */
  public static getApiConsumerUsageQuote({
    slug,
    consumerId,
    since,
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose priced usage is returned.
     */
    consumerId: string,
    /**
     * Optional RFC3339 lower bound for the quote period; defaults to the trailing 30 days.
     */
    since?: string,
    /**
     * Optional RFC3339 exclusive upper bound for the quote period; defaults to UTC midnight today.
     */
    until?: string,
  }): CancelablePromise<APIConsumerUsageQuoteResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage/quote',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Check one API consumer's billed usage against request telemetry.
   * Compares successful requests in the billing ledger with successful
   * requests in request telemetry, hour by hour, so usage the ledger
   * never received is visible before a statement is invoiced. Only whole
   * UTC hours that have settled (10 minutes) and that telemetry still
   * retains (14 days) are checked. Telemetry is sampled, so it proves a
   * lower bound of missing usage but cannot prove completeness of every
   * request. Read-only; nothing is stored.
   *
   * @returns APIConsumerUsageCompletenessResponse Completeness of billed usage over the checked hours.
   * @throws ApiError
   */
  public static getApiConsumerUsageCompleteness({
    slug,
    consumerId,
    since,
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose billed usage is checked.
     */
    consumerId: string,
    /**
     * RFC3339 start of the period to check, usually a statement's period_start.
     */
    since: string,
    /**
     * RFC3339 exclusive end of the period to check; at most 90 days after since.
     */
    until: string,
  }): CancelablePromise<APIConsumerUsageCompletenessResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-completeness',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List durable API consumer usage statements.
   * Returns immutable quote snapshots newest period first.
   * @returns APIConsumerUsageStatementListResponse Durable usage statements for the consumer.
   * @throws ApiError
   */
  public static listApiConsumerUsageStatements({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose durable usage statements are returned.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerUsageStatementListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Snapshot an API consumer usage quote or create a late-usage adjustment.
   * Creates an immutable, auditable statement revision for the explicit UTC-minute period. An unchanged draft replays. If usage or effective prices changed, the open draft becomes superseded and a new draft revision is created. After finalization, new units create the next revision containing only those units; no new units replay the latest revision. Finalized revisions are never rewritten.
   * @returns APIConsumerUsageStatementResponse The latest existing revision; the period has no new usage or the draft is unchanged.
   * @throws ApiError
   */
  public static createApiConsumerUsageStatement({
    slug,
    consumerId,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose durable usage statements are returned.
     */
    consumerId: string,
    requestBody: CreateAPIConsumerUsageStatementRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerUsageStatementResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Fetch one durable API consumer usage statement.
   * @returns APIConsumerUsageStatementResponse The immutable usage statement snapshot.
   * @throws ApiError
   */
  public static getApiConsumerUsageStatement({
    slug,
    consumerId,
    statementId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose statement is being read.
     */
    consumerId: string,
    /**
     * Durable usage statement UUID.
     */
    statementId: string,
  }): CancelablePromise<APIConsumerUsageStatementResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
        'statement_id': statementId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Finalize a fully priced API consumer usage statement.
   * Records the payable lifecycle transition; repeated calls are idempotent. A new transition emits a durable, signed usage_statement.finalized app webhook when a matching subscription exists.
   * @returns APIConsumerUsageStatementResponse The finalized usage statement.
   * @throws ApiError
   */
  public static finalizeApiConsumerUsageStatement({
    slug,
    consumerId,
    statementId,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity owning the statement.
     */
    consumerId: string,
    /**
     * Durable usage statement UUID to finalize.
     */
    statementId: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerUsageStatementResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/finalize',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
        'statement_id': statementId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Fetch a customer billing handoff receipt.
   * Returns the immutable external invoice reference recorded for a finalized usage statement.
   * @returns APIConsumerUsageStatementHandoffResponse The immutable billing handoff receipt.
   * @throws ApiError
   */
  public static getApiConsumerUsageStatementHandoff({
    slug,
    consumerId,
    statementId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose billing handoff is being recorded.
     */
    consumerId: string,
    /**
     * Durable usage statement UUID to hand off.
     */
    statementId: string,
  }): CancelablePromise<APIConsumerUsageStatementHandoffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/handoff',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
        'statement_id': statementId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Record a customer billing handoff for a finalized statement.
   * Claims a finalized usage statement for the customer's own billing system. Repeating the same claim returns the original receipt; a statement or external invoice ID cannot be claimed twice.
   * @returns APIConsumerUsageStatementHandoffResponse The existing billing handoff receipt.
   * @throws ApiError
   */
  public static claimApiConsumerUsageStatement({
    slug,
    consumerId,
    statementId,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose billing handoff is being recorded.
     */
    consumerId: string,
    /**
     * Durable usage statement UUID to hand off.
     */
    statementId: string,
    requestBody: ClaimAPIConsumerUsageStatementRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerUsageStatementHandoffResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/handoff',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
        'statement_id': statementId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List an app's immutable API consumer rate cards.
   * @returns APIConsumerRateCardListResponse Rate-card history in effective-time order.
   * @throws ApiError
   */
  public static listApiConsumerRateCards({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<APIConsumerRateCardListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/rate-cards',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Publish a new immutable API consumer rate card.
   * Publishes an app-level request price. Rate cards are append-only and
   * must use one currency per app. If effective_from is omitted, the card
   * starts at the next UTC minute.
   *
   * @returns APIConsumerRateCardResponse The newly published rate card.
   * @throws ApiError
   */
  public static createApiConsumerRateCard({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAPIConsumerRateCardRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerRateCardResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/rate-cards',
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
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List an app's consumer plans.
   * @returns APIConsumerPlanListResponse Consumer plans by name.
   * @throws ApiError
   */
  public static listApiConsumerPlans({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<APIConsumerPlanListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumer-plans',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Create a named consumer plan.
   * A plan bundles enforcement limits with its own rate-card history
   * (rate cards created with plan_id). App-wide rate cards are the default
   * plan for consumers without an assignment. At most 20 plans per app.
   *
   * @returns APIConsumerPlanResponse The new plan.
   * @throws ApiError
   */
  public static createApiConsumerPlan({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAPIConsumerPlanRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerPlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumer-plans',
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
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Replace a consumer plan's limits.
   * Limits are enforcement, not prices, so they change in place; the gateway applies them within 15 seconds. Prices change through new rate-card versions.
   * @returns APIConsumerPlanResponse The updated plan.
   * @throws ApiError
   */
  public static updateApiConsumerPlanLimits({
    slug,
    planId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer plan UUID.
     */
    planId: string,
    requestBody: UpdateAPIConsumerPlanLimitsRequest,
  }): CancelablePromise<APIConsumerPlanResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/consumer-plans/{plan_id}',
      path: {
        'slug': slug,
        'plan_id': planId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List a consumer's plan assignments, oldest first.
   * @returns APIConsumerPlanAssignmentListResponse Append-only plan history.
   * @throws ApiError
   */
  public static listApiConsumerPlanAssignments({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose plan assignments are listed or appended.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerPlanAssignmentListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/plan-assignments',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Move a consumer onto a plan from a UTC minute.
   * The assignment takes effect from effective_from (default the next minute; never in the past) and requires the target plan to have a rate card in force then. Statements price each minute with the plan in force; monthly allowances and tiers keep counting across the change. An empty plan_id returns the consumer to the default plan.
   * @returns APIConsumerPlanAssignmentResponse The new assignment.
   * @throws ApiError
   */
  public static assignApiConsumerPlan({
    slug,
    consumerId,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose plan assignments are listed or appended.
     */
    consumerId: string,
    requestBody: AssignAPIConsumerPlanRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerPlanAssignmentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/plan-assignments',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Fetch one API consumer rate card.
   * @returns APIConsumerRateCardResponse The requested immutable rate card.
   * @throws ApiError
   */
  public static getApiConsumerRateCard({
    slug,
    rateCardId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Immutable API consumer rate-card UUID.
     */
    rateCardId: string,
  }): CancelablePromise<APIConsumerRateCardResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/rate-cards/{rate_card_id}',
      path: {
        'slug': slug,
        'rate_card_id': rateCardId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List credentials for an API consumer (secret omitted).
   * @returns ConsumerKeyListResponse Consumer keys without plaintext secrets.
   * @throws ApiError
   */
  public static listConsumerKeys({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * API consumer identity UUID owning these credentials.
     */
    consumerId: string,
  }): CancelablePromise<ConsumerKeyListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/keys',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Issue a new consumer credential. Plaintext is returned once.
   * @returns ConsumerKeyResponse The created credential; key is returned exactly once.
   * @throws ApiError
   */
  public static createConsumerKey({
    slug,
    consumerId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * API consumer identity UUID owning these credentials.
     */
    consumerId: string,
    requestBody: CreateConsumerKeyRequest,
  }): CancelablePromise<ConsumerKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/keys',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
      },
    });
  }
  /**
   * Revoke a consumer credential.
   * @returns ConsumerKeyResponse The revoked credential (secret omitted).
   * @throws ApiError
   */
  public static revokeConsumerKey({
    slug,
    consumerId,
    keyId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * API consumer identity UUID for the key being revoked.
     */
    consumerId: string,
    /**
     * Consumer credential UUID.
     */
    keyId: string,
  }): CancelablePromise<ConsumerKeyResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/keys/{key_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
        'key_id': keyId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
}
