/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectStorageUsageReport } from '../models/ObjectStorageUsageReport.js';
import type { OperatorRuntimeConfig } from '../models/OperatorRuntimeConfig.js';
import type { OperatorRuntimeConfigOperation } from '../models/OperatorRuntimeConfigOperation.js';
import type { OperatorRuntimeConfigRevision } from '../models/OperatorRuntimeConfigRevision.js';
import type { Problem } from '../models/Problem.js';
import type { RollbackOperatorRuntimeConfigRequest } from '../models/RollbackOperatorRuntimeConfigRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OperatorService {
  /**
   * Import a cumulative provider object storage usage report
   * Operator session with recent step-up and Idempotency-Key required. Identical reports are idempotent; conflicting or regressing reports are rejected. Automated exporters use the operator-owned usage_reports_path backend setting.
   * @returns Problem Access denied, invalid or conflicting report, or accounting unavailable
   * @throws ApiError
   */
  public static recordObjectStorageUsage({
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Unique operation identifier.
     */
    idempotencyKey: string,
    requestBody: ObjectStorageUsageReport,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/object-storage/usage-reports',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * List operator runtime configuration
   * Returns the closed configuration catalog together with desired and
   * effective values. Sensitive bootstrap settings are redacted. This is
   * an operator-only route and is not part of the customer API.
   *
   * @returns any Runtime configuration catalog
   * @throws ApiError
   */
  public static listOperatorRuntimeConfig(): CancelablePromise<{
    items: Array<OperatorRuntimeConfig>;
    generated_at: string;
  }> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/config',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
      },
    });
  }
  /**
   * Update an operator runtime setting
   * Updates a catalogued setting without an SSH session. Hot settings are
   * applied immediately; graceful settings return a durable asynchronous
   * operation. The write is versioned, audited, persisted in PostgreSQL,
   * and propagated over pg_notify. Bootstrap, rolling, and break-glass
   * settings remain deployment-managed until their corresponding
   * controller is available.
   *
   * @returns OperatorRuntimeConfig Applied configuration entry
   * @returns OperatorRuntimeConfigOperation Graceful apply operation queued or blocked
   * @throws ApiError
   */
  public static updateOperatorRuntimeConfig({
    key,
    requestBody,
  }: {
    /**
     * Catalog key to update.
     */
    key: string,
    requestBody: {
      value: any;
      reason: string;
      expected_version?: number;
    },
  }): CancelablePromise<OperatorRuntimeConfig | OperatorRuntimeConfigOperation> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/admin/config/{key}',
      path: {
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read a runtime configuration apply operation
   * Polls the durable operation created for a graceful, rolling, or
   * break-glass configuration change. A terminal status always includes
   * the controller phase and any failure/block reason.
   *
   * @returns OperatorRuntimeConfigOperation Runtime configuration apply operation
   * @throws ApiError
   */
  public static getOperatorRuntimeConfigOperation({
    id,
  }: {
    /**
     * Durable configuration operation id.
     */
    id: string,
  }): CancelablePromise<OperatorRuntimeConfigOperation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/config-operations/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Roll back a hot runtime setting to a previous revision
   * Applies the selected historical value as a new revision through the
   * same zero-downtime hot-apply path as PATCH. Only mutable hot settings
   * are eligible. The request is optimistic-concurrency protected and
   * the rollback itself is appended to the audit and revision history.
   *
   * @returns OperatorRuntimeConfig Rolled-back and applied configuration entry
   * @throws ApiError
   */
  public static rollbackOperatorRuntimeConfig({
    key,
    requestBody,
  }: {
    /**
     * Catalog key to roll back.
     */
    key: string,
    requestBody: RollbackOperatorRuntimeConfigRequest,
  }): CancelablePromise<OperatorRuntimeConfig> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/config/{key}/rollback',
      path: {
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List runtime configuration revisions
   * Read-only append-only version history for one catalogued setting.
   * @returns any Configuration revision history
   * @throws ApiError
   */
  public static listOperatorRuntimeConfigRevisions({
    key,
    limit = 50,
  }: {
    /**
     * Catalog key whose history should be returned.
     */
    key: string,
    /**
     * Maximum number of revisions to return.
     */
    limit?: number,
  }): CancelablePromise<{
    items: Array<OperatorRuntimeConfigRevision>;
  }> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/config/{key}/revisions',
      path: {
        'key': key,
      },
      query: {
        'limit': limit,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
}
