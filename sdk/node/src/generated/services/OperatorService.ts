/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresAccountingDiagnosticsResponse } from '../models/ManagedPostgresAccountingDiagnosticsResponse.js';
import type { ManagedPostgresAccountingReconciliationRequest } from '../models/ManagedPostgresAccountingReconciliationRequest.js';
import type { ManagedPostgresAccountingReconciliationResult } from '../models/ManagedPostgresAccountingReconciliationResult.js';
import type { ManagedPostgresUsageImportRequest } from '../models/ManagedPostgresUsageImportRequest.js';
import type { ManagedPostgresUsageImportResult } from '../models/ManagedPostgresUsageImportResult.js';
import type { ManagedPostgresUsageOperatorResponse } from '../models/ManagedPostgresUsageOperatorResponse.js';
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
   * Read normalized managed PostgreSQL usage for an account
   * Operator-only view with effective guardrail ceilings and internal COGS line items. Provider IDs and credentials are never returned.
   * @returns ManagedPostgresUsageOperatorResponse Account usage and internal normalized ledger lines; Cache-Control no-store
   * @returns Problem Access denied, account not found, or accounting unavailable
   * @throws ApiError
   */
  public static getManagedPostgresUsageOperator({
    accountId,
  }: {
    /**
     * Account whose normalized PostgreSQL usage is requested.
     */
    accountId: string,
  }): CancelablePromise<ManagedPostgresUsageOperatorResponse | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/managed-postgres/usage/{account_id}',
      path: {
        'account_id': accountId,
      },
    });
  }
  /**
   * Explain managed PostgreSQL accounting blockers
   * Operator allowlist and admin scope required, with the existing session MFA gate (bearer API keys follow IAM policy). Reads local catalog and ledger evidence only. Each page is one database snapshot; pages are live views, not a frozen account snapshot. No provider requests, opaque provider IDs, or credentials.
   * @returns ManagedPostgresAccountingDiagnosticsResponse Bounded per-database accounting evidence; Cache-Control no-store
   * @returns Problem Access denied, invalid pagination, account not found, or accounting unavailable
   * @throws ApiError
   */
  public static listManagedPostgresAccountingDiagnostics({
    accountId,
    after,
    limit = 50,
  }: {
    /**
     * Account whose local accounting evidence is requested.
     */
    accountId: string,
    /**
     * Resume after the database ID returned as next_cursor.
     */
    after?: string,
    /**
     * Maximum databases returned in this page.
     */
    limit?: number,
  }): CancelablePromise<ManagedPostgresAccountingDiagnosticsResponse | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/managed-postgres/accounting/{account_id}',
      path: {
        'account_id': accountId,
      },
      query: {
        'after': after,
        'limit': limit,
      },
    });
  }
  /**
   * Preview retained managed PostgreSQL usage evidence
   * Operator allowlist and admin scope required, with the existing session MFA gate. Validates normalized complete windows against local catalog and ledger evidence. Makes no provider calls or writes. Returns the revision required for apply; concurrent ledger or lifecycle changes invalidate it.
   * @returns ManagedPostgresUsageImportResult Validated preview, without accounting changes; Cache-Control no-store
   * @returns Problem Access denied, invalid evidence, conflicting ledger, or unavailable backend
   * @throws ApiError
   */
  public static previewManagedPostgresUsageImport({
    accountId,
    requestBody,
  }: {
    /**
     * Account whose retained usage evidence will be previewed.
     */
    accountId: string,
    /**
     * Normalized retained readings and source attestation for preview.
     */
    requestBody: ManagedPostgresUsageImportRequest,
  }): CancelablePromise<ManagedPostgresUsageImportResult | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/managed-postgres/accounting/{account_id}/usage-imports/preview',
      path: {
        'account_id': accountId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Apply retained managed PostgreSQL usage evidence
   * Allowlisted operator session with recent MFA step-up, same-origin checks, and Idempotency-Key required. Bearer API keys cannot apply imports. Requires expected_revision from preview. Commits normalized ledger windows, contiguous coverage, and immutable before/after evidence atomically. import_id durably deduplicates an identical request by the same actor; conflicting reuse is rejected. Evidence observation times are preserved. This neither establishes missing identities or shutdown nor reconciles a final provider invoice.
   * @returns ManagedPostgresUsageImportResult Applied import or original committed response on identical durable replay; Cache-Control no-store
   * @returns Problem Access denied, invalid evidence, expired preview, conflicting replay, or unavailable backend
   * @throws ApiError
   */
  public static applyManagedPostgresUsageImport({
    accountId,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Account owning the reviewed database and imported usage.
     */
    accountId: string,
    /**
     * Request replay key; import_id additionally provides permanent receipt deduplication.
     */
    idempotencyKey: string,
    /**
     * Reviewed retained readings with the revision returned by preview.
     */
    requestBody: ManagedPostgresUsageImportRequest,
  }): CancelablePromise<ManagedPostgresUsageImportResult | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/managed-postgres/accounting/{account_id}/usage-imports',
      path: {
        'account_id': accountId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Preview legacy PostgreSQL identity and shutdown reconciliation
   * Operator allowlist and admin scope with existing MFA read policy required. Validates retained evidence against local catalog and ledger state without provider calls or writes. Only deleted accountable resources with unknown identities are eligible. Returns the revision required for apply.
   * @returns ManagedPostgresAccountingReconciliationResult Reconciliation preview with no catalog or accounting changes; Cache-Control no-store
   * @returns Problem Access denied, invalid evidence, incompatible ledger, conflicting identity, or unavailable backend
   * @throws ApiError
   */
  public static previewManagedPostgresAccountingReconciliation({
    accountId,
    requestBody,
  }: {
    /**
     * Account owning the legacy database being reconciled.
     */
    accountId: string,
    /**
     * Operator-verified identity and actual shutdown evidence; maximum 32 KiB.
     */
    requestBody: ManagedPostgresAccountingReconciliationRequest,
  }): CancelablePromise<ManagedPostgresAccountingReconciliationResult | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/managed-postgres/accounting/{account_id}/reconciliations/preview',
      path: {
        'account_id': accountId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Reconcile a legacy PostgreSQL identity and confirmed shutdown
   * Allowlisted operator session with recent MFA step-up, same-origin checks and Idempotency-Key required. Bearer keys cannot apply. Requires a current expected_revision. Atomically attaches the evidence-backed identity and actual shutdown boundary, resets derived coverage and retains immutable before/after evidence. Existing usage quantities and accounting obligations remain intact. Recovery and final corrections remain required; this does not settle an invoice. reconciliation_id permanently deduplicates identical requests by the same actor.
   * @returns ManagedPostgresAccountingReconciliationResult Committed reconciliation or original durable response; Cache-Control no-store
   * @returns Problem Access denied, invalid evidence, stale preview, conflicting identity/replay, or unavailable backend
   * @throws ApiError
   */
  public static applyManagedPostgresAccountingReconciliation({
    accountId,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Account owning the reviewed legacy database.
     */
    accountId: string,
    /**
     * Request replay key; reconciliation_id also provides permanent deduplication.
     */
    idempotencyKey: string,
    /**
     * Reviewed identity and shutdown evidence with preview revision; maximum 32 KiB.
     */
    requestBody: ManagedPostgresAccountingReconciliationRequest,
  }): CancelablePromise<ManagedPostgresAccountingReconciliationResult | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/managed-postgres/accounting/{account_id}/reconciliations',
      path: {
        'account_id': accountId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
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
