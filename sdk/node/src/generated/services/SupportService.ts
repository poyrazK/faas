/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CloseSupportTicketRequest } from '../models/CloseSupportTicketRequest.js';
import type { CreateSupportTicketRequest } from '../models/CreateSupportTicketRequest.js';
import type { ReopenSupportTicketRequest } from '../models/ReopenSupportTicketRequest.js';
import type { SupportMessagePage } from '../models/SupportMessagePage.js';
import type { SupportMessageRequest } from '../models/SupportMessageRequest.js';
import type { SupportSatisfaction } from '../models/SupportSatisfaction.js';
import type { SupportSatisfactionRequest } from '../models/SupportSatisfactionRequest.js';
import type { SupportSatisfactionSubmission } from '../models/SupportSatisfactionSubmission.js';
import type { SupportSubmission } from '../models/SupportSubmission.js';
import type { SupportTicket } from '../models/SupportTicket.js';
import type { SupportTicketPage } from '../models/SupportTicketPage.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class SupportService {
  /**
   * List personal support cases
   * Personal support list operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportTicketPage Current scoped support records. Result of personalSupportList.
   * @throws ApiError
   */
  public static personalSupportList({
    cursor,
    status,
    search,
  }: {
    /**
     * Last ticket UUID from the preceding page. This parameter belongs to personalSupportList.
     */
    cursor?: string,
    /**
     * Filter case status. This parameter belongs to personalSupportList.
     */
    status?: 'open' | 'pending' | 'resolved',
    /**
     * Search subject and currently visible public conversation text. This parameter belongs to personalSupportList.
     */
    search?: string,
  }): CancelablePromise<SupportTicketPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/support/tickets',
      query: {
        'cursor': cursor,
        'status': status,
        'search': search,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Create personal support case workflow
   * Personal support create operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of personalSupportCreate.
   * @throws ApiError
   */
  public static personalSupportCreate({
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to personalSupportCreate.
     */
    idempotencyKey: string,
    requestBody: CreateSupportTicketRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/support/tickets',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Get personal support case workflow
   * Personal support get operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportTicket Current scoped support records. Result of personalSupportGet.
   * @throws ApiError
   */
  public static personalSupportGet({
    ticket,
  }: {
    /**
     * Support case UUID in this scope. This parameter belongs to personalSupportGet.
     */
    ticket: string,
  }): CancelablePromise<SupportTicket> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/support/tickets/{ticket}',
      path: {
        'ticket': ticket,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Messages personal support case workflow
   * Personal support messages operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportMessagePage Current scoped support records. Result of personalSupportMessages.
   * @throws ApiError
   */
  public static personalSupportMessages({
    ticket,
    after,
  }: {
    /**
     * Support case UUID in this scope. This parameter belongs to personalSupportMessages.
     */
    ticket: string,
    /**
     * Return messages after this global message sequence. This parameter belongs to personalSupportMessages.
     */
    after?: number,
  }): CancelablePromise<SupportMessagePage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/support/tickets/{ticket}/messages',
      path: {
        'ticket': ticket,
      },
      query: {
        'after': after,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Reply personal support case workflow
   * Personal support reply operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of personalSupportReply.
   * @throws ApiError
   */
  public static personalSupportReply({
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Support case UUID in this scope. This parameter belongs to personalSupportReply.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to personalSupportReply.
     */
    idempotencyKey: string,
    requestBody: SupportMessageRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/support/tickets/{ticket}/messages',
      path: {
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Close personal support case workflow
   * Personal support close operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of personalSupportClose.
   * @throws ApiError
   */
  public static personalSupportClose({
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Support case UUID in this scope. This parameter belongs to personalSupportClose.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to personalSupportClose.
     */
    idempotencyKey: string,
    requestBody: CloseSupportTicketRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/support/tickets/{ticket}/close',
      path: {
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Reopen personal support case workflow
   * Personal support reopen operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of personalSupportReopen.
   * @throws ApiError
   */
  public static personalSupportReopen({
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Support case UUID in this scope. This parameter belongs to personalSupportReopen.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to personalSupportReopen.
     */
    idempotencyKey: string,
    requestBody: ReopenSupportTicketRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/support/tickets/{ticket}/reopen',
      path: {
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Survey personal support case workflow
   * Personal support survey operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSatisfaction Current scoped support records. Result of personalSupportSurvey.
   * @throws ApiError
   */
  public static personalSupportSurvey({
    ticket,
  }: {
    /**
     * Support case UUID in this scope. This parameter belongs to personalSupportSurvey.
     */
    ticket: string,
  }): CancelablePromise<SupportSatisfaction> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/support/tickets/{ticket}/satisfaction',
      path: {
        'ticket': ticket,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Rate personal support case workflow
   * Personal support rate operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSatisfactionSubmission Support transaction accepted. Result of personalSupportRate.
   * @throws ApiError
   */
  public static personalSupportRate({
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Support case UUID in this scope. This parameter belongs to personalSupportRate.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to personalSupportRate.
     */
    idempotencyKey: string,
    requestBody: SupportSatisfactionRequest,
  }): CancelablePromise<SupportSatisfactionSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/support/tickets/{ticket}/satisfaction',
      path: {
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * List organization support cases
   * Organization support list operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportTicketPage Current scoped support records. Result of organizationSupportList.
   * @throws ApiError
   */
  public static organizationSupportList({
    org,
    cursor,
    status,
    search,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportList.
     */
    org: string,
    /**
     * Last ticket UUID from the preceding page. This parameter belongs to organizationSupportList.
     */
    cursor?: string,
    /**
     * Filter case status. This parameter belongs to organizationSupportList.
     */
    status?: 'open' | 'pending' | 'resolved',
    /**
     * Search subject and currently visible public conversation text. This parameter belongs to organizationSupportList.
     */
    search?: string,
  }): CancelablePromise<SupportTicketPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{org}/support/tickets',
      path: {
        'org': org,
      },
      query: {
        'cursor': cursor,
        'status': status,
        'search': search,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Create organization support case workflow
   * Organization support create operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of organizationSupportCreate.
   * @throws ApiError
   */
  public static organizationSupportCreate({
    org,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportCreate.
     */
    org: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to organizationSupportCreate.
     */
    idempotencyKey: string,
    requestBody: CreateSupportTicketRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{org}/support/tickets',
      path: {
        'org': org,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Get organization support case workflow
   * Organization support get operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportTicket Current scoped support records. Result of organizationSupportGet.
   * @throws ApiError
   */
  public static organizationSupportGet({
    org,
    ticket,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportGet.
     */
    org: string,
    /**
     * Support case UUID in this scope. This parameter belongs to organizationSupportGet.
     */
    ticket: string,
  }): CancelablePromise<SupportTicket> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{org}/support/tickets/{ticket}',
      path: {
        'org': org,
        'ticket': ticket,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Messages organization support case workflow
   * Organization support messages operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportMessagePage Current scoped support records. Result of organizationSupportMessages.
   * @throws ApiError
   */
  public static organizationSupportMessages({
    org,
    ticket,
    after,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportMessages.
     */
    org: string,
    /**
     * Support case UUID in this scope. This parameter belongs to organizationSupportMessages.
     */
    ticket: string,
    /**
     * Return messages after this global message sequence. This parameter belongs to organizationSupportMessages.
     */
    after?: number,
  }): CancelablePromise<SupportMessagePage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{org}/support/tickets/{ticket}/messages',
      path: {
        'org': org,
        'ticket': ticket,
      },
      query: {
        'after': after,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Reply organization support case workflow
   * Organization support reply operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of organizationSupportReply.
   * @throws ApiError
   */
  public static organizationSupportReply({
    org,
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportReply.
     */
    org: string,
    /**
     * Support case UUID in this scope. This parameter belongs to organizationSupportReply.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to organizationSupportReply.
     */
    idempotencyKey: string,
    requestBody: SupportMessageRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{org}/support/tickets/{ticket}/messages',
      path: {
        'org': org,
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Close organization support case workflow
   * Organization support close operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of organizationSupportClose.
   * @throws ApiError
   */
  public static organizationSupportClose({
    org,
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportClose.
     */
    org: string,
    /**
     * Support case UUID in this scope. This parameter belongs to organizationSupportClose.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to organizationSupportClose.
     */
    idempotencyKey: string,
    requestBody: CloseSupportTicketRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{org}/support/tickets/{ticket}/close',
      path: {
        'org': org,
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Reopen organization support case workflow
   * Organization support reopen operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSubmission Support transaction accepted. Result of organizationSupportReopen.
   * @throws ApiError
   */
  public static organizationSupportReopen({
    org,
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportReopen.
     */
    org: string,
    /**
     * Support case UUID in this scope. This parameter belongs to organizationSupportReopen.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to organizationSupportReopen.
     */
    idempotencyKey: string,
    requestBody: ReopenSupportTicketRequest,
  }): CancelablePromise<SupportSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{org}/support/tickets/{ticket}/reopen',
      path: {
        'org': org,
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Survey organization support case workflow
   * Organization support survey operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSatisfaction Current scoped support records. Result of organizationSupportSurvey.
   * @throws ApiError
   */
  public static organizationSupportSurvey({
    org,
    ticket,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportSurvey.
     */
    org: string,
    /**
     * Support case UUID in this scope. This parameter belongs to organizationSupportSurvey.
     */
    ticket: string,
  }): CancelablePromise<SupportSatisfaction> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{org}/support/tickets/{ticket}/satisfaction',
      path: {
        'org': org,
        'ticket': ticket,
      },
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
  /**
   * Rate organization support case workflow
   * Organization support rate operation. Responses omit private notes, staff queues and engineering details. Writes require current verified access, the expected revision where applicable, and a UUID idempotency key.
   * @returns SupportSatisfactionSubmission Support transaction accepted. Result of organizationSupportRate.
   * @throws ApiError
   */
  public static organizationSupportRate({
    org,
    ticket,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Organization slug; current owner or administrator membership is required. This parameter belongs to organizationSupportRate.
     */
    org: string,
    /**
     * Support case UUID in this scope. This parameter belongs to organizationSupportRate.
     */
    ticket: string,
    /**
     * Client-generated UUID bound permanently to the actor, operation, resource and JSON request. Reuse it for retries. This parameter belongs to organizationSupportRate.
     */
    idempotencyKey: string,
    requestBody: SupportSatisfactionRequest,
  }): CancelablePromise<SupportSatisfactionSubmission> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{org}/support/tickets/{ticket}/satisfaction',
      path: {
        'org': org,
        'ticket': ticket,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Support request fields do not satisfy the customer assistance contract.`,
        401: `Authentication is required to access customer assistance records.`,
        403: `Current verified account or organization support access is required.`,
        404: `The requested assistance record is unavailable in the current scope.`,
        409: `The assistance revision, reopening allowance or request receipt conflicts with this command.`,
        413: `The customer assistance JSON request exceeds its byte limit.`,
        429: `The rolling assistance submission or mutation allowance is exhausted.`,
        503: `Customer assistance storage is temporarily unavailable; retry the same request key.`,
      },
    });
  }
}
