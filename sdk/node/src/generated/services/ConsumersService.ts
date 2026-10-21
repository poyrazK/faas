/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerBillingPolicyResponse } from '../models/APIConsumerBillingPolicyResponse.js';
import type { APIConsumerCreditListResponse } from '../models/APIConsumerCreditListResponse.js';
import type { APIConsumerCreditResponse } from '../models/APIConsumerCreditResponse.js';
import type { APIConsumerListResponse } from '../models/APIConsumerListResponse.js';
import type { APIConsumerOverdueHoldListResponse } from '../models/APIConsumerOverdueHoldListResponse.js';
import type { APIConsumerPlanAssignmentListResponse } from '../models/APIConsumerPlanAssignmentListResponse.js';
import type { APIConsumerPlanAssignmentResponse } from '../models/APIConsumerPlanAssignmentResponse.js';
import type { APIConsumerPlanListResponse } from '../models/APIConsumerPlanListResponse.js';
import type { APIConsumerPlanResponse } from '../models/APIConsumerPlanResponse.js';
import type { APIConsumerRateCardListResponse } from '../models/APIConsumerRateCardListResponse.js';
import type { APIConsumerRateCardResponse } from '../models/APIConsumerRateCardResponse.js';
import type { APIConsumerResponse } from '../models/APIConsumerResponse.js';
import type { APIConsumerSpendCapResponse } from '../models/APIConsumerSpendCapResponse.js';
import type { APIConsumerTrialResponse } from '../models/APIConsumerTrialResponse.js';
import type { APIConsumerUsageAlertListResponse } from '../models/APIConsumerUsageAlertListResponse.js';
import type { APIConsumerUsageCompletenessResponse } from '../models/APIConsumerUsageCompletenessResponse.js';
import type { APIConsumerUsageQuoteResponse } from '../models/APIConsumerUsageQuoteResponse.js';
import type { APIConsumerUsageResponse } from '../models/APIConsumerUsageResponse.js';
import type { APIConsumerUsageStatementHandoffResponse } from '../models/APIConsumerUsageStatementHandoffResponse.js';
import type { APIConsumerUsageStatementListResponse } from '../models/APIConsumerUsageStatementListResponse.js';
import type { APIConsumerUsageStatementResponse } from '../models/APIConsumerUsageStatementResponse.js';
import type { AssignAPIConsumerPlanRequest } from '../models/AssignAPIConsumerPlanRequest.js';
import type { ClaimAPIConsumerUsageStatementRequest } from '../models/ClaimAPIConsumerUsageStatementRequest.js';
import type { CompleteConsumerSignupRequest } from '../models/CompleteConsumerSignupRequest.js';
import type { CompleteConsumerSignupResponse } from '../models/CompleteConsumerSignupResponse.js';
import type { ConsumerBillingStatementsResponse } from '../models/ConsumerBillingStatementsResponse.js';
import type { ConsumerBillingSummaryResponse } from '../models/ConsumerBillingSummaryResponse.js';
import type { ConsumerBillingUsageResponse } from '../models/ConsumerBillingUsageResponse.js';
import type { ConsumerKeyListResponse } from '../models/ConsumerKeyListResponse.js';
import type { ConsumerKeyResponse } from '../models/ConsumerKeyResponse.js';
import type { ConsumerSignupFormResponse } from '../models/ConsumerSignupFormResponse.js';
import type { ConsumerSignupListResponse } from '../models/ConsumerSignupListResponse.js';
import type { ConsumerSignupSettingsResponse } from '../models/ConsumerSignupSettingsResponse.js';
import type { CreateAPIConsumerCreditRequest } from '../models/CreateAPIConsumerCreditRequest.js';
import type { CreateAPIConsumerPlanRequest } from '../models/CreateAPIConsumerPlanRequest.js';
import type { CreateAPIConsumerRateCardRequest } from '../models/CreateAPIConsumerRateCardRequest.js';
import type { CreateAPIConsumerRequest } from '../models/CreateAPIConsumerRequest.js';
import type { CreateAPIConsumerUsageStatementRequest } from '../models/CreateAPIConsumerUsageStatementRequest.js';
import type { CreateConsumerKeyRequest } from '../models/CreateConsumerKeyRequest.js';
import type { CreateConsumerSignupRequest } from '../models/CreateConsumerSignupRequest.js';
import type { CreateConsumerSignupResponse } from '../models/CreateConsumerSignupResponse.js';
import type { CreateStatementInvoiceRequest } from '../models/CreateStatementInvoiceRequest.js';
import type { PutAPIConsumerBillingPolicyRequest } from '../models/PutAPIConsumerBillingPolicyRequest.js';
import type { PutAPIConsumerSpendCapRequest } from '../models/PutAPIConsumerSpendCapRequest.js';
import type { PutAPIConsumerTrialRequest } from '../models/PutAPIConsumerTrialRequest.js';
import type { PutConsumerSignupRequest } from '../models/PutConsumerSignupRequest.js';
import type { StatementInvoiceExportResponse } from '../models/StatementInvoiceExportResponse.js';
import type { UpdateAPIConsumerPlanLimitsRequest } from '../models/UpdateAPIConsumerPlanLimitsRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ConsumersService {
  /**
   * Read your own month-to-date usage and spend with your consumer key (ADR-1506).
   * For an app's customers, not operators. Authenticate with a consumer
   * key that carries the billing scope. The amount is priced like the
   * next statement, but late usage can still change it.
   *
   * @returns ConsumerBillingSummaryResponse The consumer's month to date.
   * @throws ApiError
   */
  public static getConsumerBillingSummary({
    slug,
  }: {
    /**
     * The app whose consumer key authenticates the request.
     */
    slug: string,
  }): CancelablePromise<ConsumerBillingSummaryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/consumer-billing/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `The consumer key lacks the billing scope.`,
        429: `The key exceeded 60 billing requests per minute (\`consumer_billing_rate_limited\`).`,
      },
    });
  }
  /**
   * Read your own daily usage with your consumer key.
   * @returns ConsumerBillingUsageResponse Requests and billable units per UTC day.
   * @throws ApiError
   */
  public static getConsumerBillingUsage({
    slug,
    since,
    until,
  }: {
    /**
     * The app whose usage is read.
     */
    slug: string,
    /**
     * RFC 3339 start (default the start of the current UTC month).
     */
    since?: string,
    /**
     * RFC 3339 end, at most 31 days after since (default now).
     */
    until?: string,
  }): CancelablePromise<ConsumerBillingUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/consumer-billing/{slug}/usage',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `This consumer key has no billing scope.`,
        429: `Too many billing requests for this key.`,
      },
    });
  }
  /**
   * Read your own finalized statements and invoice status with your consumer key.
   * @returns ConsumerBillingStatementsResponse The latest 24 finalized statements, newest first, each with its invoice's payment status.
   * @throws ApiError
   */
  public static listConsumerBillingStatements({
    slug,
  }: {
    /**
     * The app whose statements are read.
     */
    slug: string,
  }): CancelablePromise<ConsumerBillingStatementsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/consumer-billing/{slug}/statements',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `A consumer key without the billing scope cannot list statements.`,
        429: `Statement reads for this key are rate limited.`,
      },
    });
  }
  /**
   * Read an app's public sign-up form (ADR-20261021143000).
   * For an app's prospective customers; needs no credential.
   * @returns ConsumerSignupFormResponse The app's display name, offered plans and their current prices.
   * @throws ApiError
   */
  public static getConsumerSignupForm({
    slug,
  }: {
    /**
     * The app customers sign up for.
     */
    slug: string,
  }): CancelablePromise<ConsumerSignupFormResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/consumer-signup/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        404: `code: not_found`,
        429: `Too many requests from this address (\`consumer_signup_rate_limited\`).`,
      },
    });
  }
  /**
   * Start a sign-up and get a Stripe Checkout URL to save a card.
   * Needs no credential. Creates a Stripe customer in the app owner's
   * Stripe account and a setup-mode Checkout Session; nothing is charged.
   * The claim token is returned only here and completes the sign-up
   * within 24 hours.
   *
   * @returns CreateConsumerSignupResponse The sign-up, its Checkout URL and claim token.
   * @throws ApiError
   */
  public static createConsumerSignup({
    slug,
    requestBody,
  }: {
    /**
     * The app customers sign up for.
     */
    slug: string,
    requestBody: CreateConsumerSignupRequest,
  }): CancelablePromise<CreateConsumerSignupResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/consumer-signup/{slug}',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        422: `An invalid email or name, or a plan that is not offered.`,
        429: `Too many sign-ups from this address (\`consumer_signup_rate_limited\`) or for the app today (\`consumer_signup_daily_limit\`).`,
        502: `Stripe could not be reached (\`billing_provider_unavailable\`).`,
        503: `The app's billing setup cannot take sign-ups (\`consumer_signup_unavailable\`).`,
      },
    });
  }
  /**
   * Complete a sign-up once the card is saved and get a consumer key.
   * Needs no credential: the claim token proves the sign-up. Each call
   * returns a new key and revokes the one the sign-up issued before.
   *
   * @returns CompleteConsumerSignupResponse The customer's consumer and its new key, shown only here.
   * @throws ApiError
   */
  public static completeConsumerSignup({
    slug,
    signup,
    requestBody,
  }: {
    /**
     * The app the sign-up is for.
     */
    slug: string,
    /**
     * The sign-up to complete.
     */
    signup: string,
    requestBody: CompleteConsumerSignupRequest,
  }): CancelablePromise<CompleteConsumerSignupResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/consumer-signup/{slug}/signups/{signup}/complete',
      path: {
        'slug': slug,
        'signup': signup,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        403: `The app owner revoked this customer.`,
        404: `code: not_found`,
        409: `The card is not saved yet (\`consumer_signup_checkout_incomplete\`).`,
        410: `The sign-up is more than 24 hours old (\`consumer_signup_expired\`).`,
        429: `Too many completions from this address (\`consumer_signup_rate_limited\`).`,
        502: `Stripe could not be reached to confirm the card (\`billing_provider_unavailable\`).`,
        503: `The app's billing setup cannot complete sign-ups (\`consumer_signup_unavailable\`).`,
      },
    });
  }
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
   * Read the app consumer statement's invoice export.
   * @returns StatementInvoiceExportResponse The app consumer statement's export.
   * @throws ApiError
   */
  public static getApiConsumerUsageStatementInvoice({
    slug,
    consumerId,
    statementId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose statement is exported.
     */
    consumerId: string,
    /**
     * Finalized statement revision to export.
     */
    statementId: string,
  }): CancelablePromise<StatementInvoiceExportResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/invoice',
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
   * Export a finalized app consumer statement as an invoice in Stripe, Paddle or Lago.
   * Exports one finalized app consumer statement revision to the
   * provider in the body (default stripe; connect it with
   * PUT /v1/account/billing-connections/{provider}), ADR-1502 and ADR-1507.
   * The connection's collection mode (ADR-1311) decides whether the
   * provider keeps a draft, sends it or charges it. One item per rate card
   * and unit price, plus a rounding item, makes the invoice equal the
   * statement total rounded to the currency's minor unit; with a negative
   * line, Paddle and Lago get a single summary item instead. The
   * statement's handoff records the provider invoice ID on success. A retry
   * resumes the same invoice, a completed export is returned as is, and a
   * statement exports to one provider only. While another attempt is
   * exporting the statement the answer is 409
   * `statement_invoice_export_in_progress`; an export the provider refused
   * before creating an invoice can be retried with another customer or
   * provider (ADR-20261019180000). Currencies with up to three decimal
   * places (ADR-20261019100330); Paddle takes USD, EUR and GBP.
   *
   * @returns StatementInvoiceExportResponse The completed app consumer statement export.
   * @throws ApiError
   */
  public static exportApiConsumerUsageStatementInvoice({
    slug,
    consumerId,
    statementId,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose statement is exported.
     */
    consumerId: string,
    /**
     * Finalized statement revision to export.
     */
    statementId: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    requestBody?: CreateStatementInvoiceRequest,
  }): CancelablePromise<StatementInvoiceExportResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/invoice',
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
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `The app consumer statement or customer cannot be invoiced, or the provider rejected the request.`,
        502: `The provider could not be reached (\`billing_provider_unavailable\`); retry to resume the app consumer statement export.`,
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
   * List a consumer's prepaid credit grants, newest first, with the usable balance per currency (ADR-1306).
   * @returns APIConsumerCreditListResponse The consumer's grants and balances.
   * @throws ApiError
   */
  public static listApiConsumerCredits({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose prepaid credit is listed or granted.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerCreditListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/credits',
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
   * Grant a consumer prepaid credit.
   * Finalized statements in the credit's currency draw it down, the grant
   * that expires first before grants that never expire, in the same
   * transaction that finalizes them (ADR-1306). Credit never pays more
   * than a statement's amount. A consumer holds at most 100 grants with
   * credit left.
   *
   * @returns APIConsumerCreditResponse The new grant.
   * @throws ApiError
   */
  public static createApiConsumerCredit({
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
     * Consumer whose prepaid credit is listed or granted.
     */
    consumerId: string,
    requestBody: CreateAPIConsumerCreditRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerCreditResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/credits',
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
        409: `The consumer already holds 100 grants with credit left.`,
        422: `Invalid currency, amount, note or expiry.`,
      },
    });
  }
  /**
   * Void what is left of a prepaid credit grant; credit already applied stays applied.
   * @returns APIConsumerCreditResponse The voided grant.
   * @throws ApiError
   */
  public static voidApiConsumerCredit({
    slug,
    consumerId,
    creditId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer that holds the credit.
     */
    consumerId: string,
    /**
     * Credit grant to void.
     */
    creditId: string,
  }): CancelablePromise<APIConsumerCreditResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/credits/{credit_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
        'credit_id': creditId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read a consumer's monthly spend cap and its last check.
   * @returns APIConsumerSpendCapResponse The cap and whether it is reached this month.
   * @throws ApiError
   */
  public static getApiConsumerSpendCap({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose monthly spend is capped.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerSpendCapResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/spend-cap',
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
   * Set a consumer's monthly spend cap (ADR-1306).
   * Once the month's amount due passes the cap, the gateway answers the
   * consumer with 402 `consumer_spend_cap_reached` until the month ends,
   * the cap rises or credit is added. The amount due is the month to
   * date priced like a statement, minus usable prepaid credit. apid
   * checks it every minute and at once when the cap or credit changes,
   * so a consumer can overshoot by about two minutes of traffic. A zero
   * cap allows prepaid usage only.
   *
   * @returns APIConsumerSpendCapResponse The stored cap, checked at once.
   * @throws ApiError
   */
  public static putApiConsumerSpendCap({
    slug,
    consumerId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose monthly spend is capped.
     */
    consumerId: string,
    requestBody: PutAPIConsumerSpendCapRequest,
  }): CancelablePromise<APIConsumerSpendCapResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/spend-cap',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `Invalid currency or cap.`,
      },
    });
  }
  /**
   * Remove a consumer's spend cap.
   * @returns void
   * @throws ApiError
   */
  public static deleteApiConsumerSpendCap({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose monthly spend is capped.
     */
    consumerId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/spend-cap',
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
   * Read a consumer's free trial (ADR-1314).
   * @returns APIConsumerTrialResponse The consumer's trial.
   * @throws ApiError
   */
  public static getApiConsumerTrial({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose free trial is read or changed.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerTrialResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/trial',
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
   * Start a consumer's one free trial, or move the end of a running one.
   * Usage in the trial is priced at zero and consumes no included units.
   * A new trial starts at the next UTC minute; a running trial's end can
   * move, never before the next minute. A consumer whose trial has ended
   * cannot get another (409).
   *
   * @returns APIConsumerTrialResponse The stored trial.
   * @throws ApiError
   */
  public static putApiConsumerTrial({
    slug,
    consumerId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose free trial is read or changed.
     */
    consumerId: string,
    requestBody: PutAPIConsumerTrialRequest,
  }): CancelablePromise<APIConsumerTrialResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/trial',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `Invalid trial length or end.`,
      },
    });
  }
  /**
   * End a consumer's running trial at the next minute.
   * @returns void
   * @throws ApiError
   */
  public static endApiConsumerTrial({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose free trial is read or changed.
     */
    consumerId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/trial',
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
   * Read the app's overdue-invoice rule (ADR-1315).
   * @returns APIConsumerBillingPolicyResponse The app's overdue rule.
   * @throws ApiError
   */
  public static getApiConsumerBillingPolicy({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<APIConsumerBillingPolicyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumer-billing-policy',
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
   * Refuse consumers whose exported invoice is unpaid some days after its due date.
   * Every 5 minutes apid places a hold on each consumer whose exported
   * invoice is open or uncollectible with an amount left at least
   * block_overdue_after_days past its due date, and lifts holds that no
   * longer apply. The gateway answers a held consumer with 402
   * `consumer_invoice_overdue`.
   *
   * @returns APIConsumerBillingPolicyResponse The stored rule.
   * @throws ApiError
   */
  public static putApiConsumerBillingPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: PutAPIConsumerBillingPolicyRequest,
  }): CancelablePromise<APIConsumerBillingPolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/consumer-billing-policy',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `Days outside 0 to 365.`,
      },
    });
  }
  /**
   * Remove the app's overdue rule; holds lift at the next check.
   * @returns void
   * @throws ApiError
   */
  public static deleteApiConsumerBillingPolicy({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumer-billing-policy',
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
   * Read the app's self-service sign-up settings (ADR-20261021143000).
   * @returns ConsumerSignupSettingsResponse The app's sign-up settings and hosted page URL.
   * @throws ApiError
   */
  public static getConsumerSignup({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ConsumerSignupSettingsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumer-signup',
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
   * Let the app's customers sign up themselves and save a card with Stripe Checkout.
   * Customers sign up on the hosted page (page_url) or through the public
   * sign-up API: they pick one of plan_ids (none offers the default
   * plan), save a card in the account's own Stripe account, and get a
   * consumer key with key_scopes. Needs the account's Stripe billing
   * connection. A restricted Stripe key needs write access to Customers
   * and Checkout Sessions and read access to SetupIntents.
   *
   * @returns ConsumerSignupSettingsResponse The stored settings.
   * @throws ApiError
   */
  public static putConsumerSignup({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: PutConsumerSignupRequest,
  }): CancelablePromise<ConsumerSignupSettingsResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/consumer-signup',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Stripe is not connected.`,
        422: `A provider other than Stripe, an unknown plan, an invalid scope or URL, or a value out of range.`,
      },
    });
  }
  /**
   * Turn sign-up off; pending sign-ups can no longer complete.
   * @returns void
   * @throws ApiError
   */
  public static deleteConsumerSignup({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumer-signup',
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
   * List the app's latest 100 sign-ups, newest first.
   * @returns ConsumerSignupListResponse The app's sign-ups.
   * @throws ApiError
   */
  public static listConsumerSignups({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ConsumerSignupListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumer-signups',
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
   * List the consumers the gateway refuses because of an overdue invoice.
   * @returns APIConsumerOverdueHoldListResponse The app's held consumers, oldest due date first.
   * @throws ApiError
   */
  public static listApiConsumerOverdueHolds({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<APIConsumerOverdueHoldListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumer-overdue-holds',
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
   * List the plan usage alerts a consumer crossed.
   * Returns up to 100 recorded crossings of the consumer's plan alert
   * thresholds, newest first. Each crossing also emitted one
   * consumer.usage_threshold webhook; this list reconciles missed
   * deliveries.
   *
   * @returns APIConsumerUsageAlertListResponse The consumer's usage alerts.
   * @throws ApiError
   */
  public static listApiConsumerUsageAlerts({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer whose usage alerts are listed.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerUsageAlertListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage-alerts',
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
