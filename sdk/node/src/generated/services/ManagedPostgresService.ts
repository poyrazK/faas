/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppPostgresBindingList } from '../models/AppPostgresBindingList.js';
import type { AppTaskResponse } from '../models/AppTaskResponse.js';
import type { CancelManagedPostgresQueryRequest } from '../models/CancelManagedPostgresQueryRequest.js';
import type { ChangeManagedPostgresComputePolicyRequest } from '../models/ChangeManagedPostgresComputePolicyRequest.js';
import type { CreateDataAPIContractTaskRequest } from '../models/CreateDataAPIContractTaskRequest.js';
import type { CreateManagedPostgresBindingRequest } from '../models/CreateManagedPostgresBindingRequest.js';
import type { CreateManagedPostgresDatabaseRequest } from '../models/CreateManagedPostgresDatabaseRequest.js';
import type { CreateManagedPostgresMaskedBranchRequest } from '../models/CreateManagedPostgresMaskedBranchRequest.js';
import type { DataAPIChangeEventBatch } from '../models/DataAPIChangeEventBatch.js';
import type { DataAPIChangeEventBatchResponse } from '../models/DataAPIChangeEventBatchResponse.js';
import type { DataAPIChangeHookReport } from '../models/DataAPIChangeHookReport.js';
import type { DataAPIConnectionBudgets } from '../models/DataAPIConnectionBudgets.js';
import type { DataAPIRuntime } from '../models/DataAPIRuntime.js';
import type { DataAPIRuntimeConnectionReport } from '../models/DataAPIRuntimeConnectionReport.js';
import type { DataAPIStatus } from '../models/DataAPIStatus.js';
import type { DataAPIUsage } from '../models/DataAPIUsage.js';
import type { DataAPIUsageReport } from '../models/DataAPIUsageReport.js';
import type { DataAPIUsageReportResponse } from '../models/DataAPIUsageReportResponse.js';
import type { LabelChange } from '../models/LabelChange.js';
import type { LabelsResponse } from '../models/LabelsResponse.js';
import type { ManagedPostgresAutoGrowStatus } from '../models/ManagedPostgresAutoGrowStatus.js';
import type { ManagedPostgresAutovacuum } from '../models/ManagedPostgresAutovacuum.js';
import type { ManagedPostgresBackupSchedule } from '../models/ManagedPostgresBackupSchedule.js';
import type { ManagedPostgresBinding } from '../models/ManagedPostgresBinding.js';
import type { ManagedPostgresBindingList } from '../models/ManagedPostgresBindingList.js';
import type { ManagedPostgresBindingTimeouts } from '../models/ManagedPostgresBindingTimeouts.js';
import type { ManagedPostgresCapabilities } from '../models/ManagedPostgresCapabilities.js';
import type { ManagedPostgresComputePolicyChange } from '../models/ManagedPostgresComputePolicyChange.js';
import type { ManagedPostgresConnectionBudget } from '../models/ManagedPostgresConnectionBudget.js';
import type { ManagedPostgresCutover } from '../models/ManagedPostgresCutover.js';
import type { ManagedPostgresCutoverPreflight } from '../models/ManagedPostgresCutoverPreflight.js';
import type { ManagedPostgresDatabase } from '../models/ManagedPostgresDatabase.js';
import type { ManagedPostgresDatabaseList } from '../models/ManagedPostgresDatabaseList.js';
import type { ManagedPostgresDiagnostics } from '../models/ManagedPostgresDiagnostics.js';
import type { ManagedPostgresErrorHistory } from '../models/ManagedPostgresErrorHistory.js';
import type { ManagedPostgresExport } from '../models/ManagedPostgresExport.js';
import type { ManagedPostgresExportManifest } from '../models/ManagedPostgresExportManifest.js';
import type { ManagedPostgresExportRequest } from '../models/ManagedPostgresExportRequest.js';
import type { ManagedPostgresExtensions } from '../models/ManagedPostgresExtensions.js';
import type { ManagedPostgresImportRequest } from '../models/ManagedPostgresImportRequest.js';
import type { ManagedPostgresImportSource } from '../models/ManagedPostgresImportSource.js';
import type { ManagedPostgresImportStatus } from '../models/ManagedPostgresImportStatus.js';
import type { ManagedPostgresMaintenanceStatus } from '../models/ManagedPostgresMaintenanceStatus.js';
import type { ManagedPostgresMigrationCheck } from '../models/ManagedPostgresMigrationCheck.js';
import type { ManagedPostgresMigrationProgress } from '../models/ManagedPostgresMigrationProgress.js';
import type { ManagedPostgresMigrationVerifyRequest } from '../models/ManagedPostgresMigrationVerifyRequest.js';
import type { ManagedPostgresOverage } from '../models/ManagedPostgresOverage.js';
import type { ManagedPostgresOverageRequest } from '../models/ManagedPostgresOverageRequest.js';
import type { ManagedPostgresQueryCancellation } from '../models/ManagedPostgresQueryCancellation.js';
import type { ManagedPostgresQueryInsights } from '../models/ManagedPostgresQueryInsights.js';
import type { ManagedPostgresQueryRequest } from '../models/ManagedPostgresQueryRequest.js';
import type { ManagedPostgresQueryResponse } from '../models/ManagedPostgresQueryResponse.js';
import type { ManagedPostgresResize } from '../models/ManagedPostgresResize.js';
import type { ManagedPostgresRestoreCheck } from '../models/ManagedPostgresRestoreCheck.js';
import type { ManagedPostgresRestoreDrill } from '../models/ManagedPostgresRestoreDrill.js';
import type { ManagedPostgresSessions } from '../models/ManagedPostgresSessions.js';
import type { ManagedPostgresSuspendTimeout } from '../models/ManagedPostgresSuspendTimeout.js';
import type { ManagedPostgresTableHealth } from '../models/ManagedPostgresTableHealth.js';
import type { ManagedPostgresUpgradeRehearsal } from '../models/ManagedPostgresUpgradeRehearsal.js';
import type { ManagedPostgresUsageResponse } from '../models/ManagedPostgresUsageResponse.js';
import type { PrepareManagedPostgresCutoverRequest } from '../models/PrepareManagedPostgresCutoverRequest.js';
import type { Problem } from '../models/Problem.js';
import type { RequestManagedPostgresRestoreDrill } from '../models/RequestManagedPostgresRestoreDrill.js';
import type { RequestManagedPostgresUpgradeRehearsal } from '../models/RequestManagedPostgresUpgradeRehearsal.js';
import type { ResizeManagedPostgresDatabaseRequest } from '../models/ResizeManagedPostgresDatabaseRequest.js';
import type { ResourceStatus } from '../models/ResourceStatus.js';
import type { RestoreManagedPostgresDatabaseRequest } from '../models/RestoreManagedPostgresDatabaseRequest.js';
import type { SetManagedPostgresAutoGrowRequest } from '../models/SetManagedPostgresAutoGrowRequest.js';
import type { SetManagedPostgresAutovacuumRequest } from '../models/SetManagedPostgresAutovacuumRequest.js';
import type { SetManagedPostgresBackupScheduleRequest } from '../models/SetManagedPostgresBackupScheduleRequest.js';
import type { SetManagedPostgresBindingTimeoutsRequest } from '../models/SetManagedPostgresBindingTimeoutsRequest.js';
import type { SetManagedPostgresErrorHistoryRequest } from '../models/SetManagedPostgresErrorHistoryRequest.js';
import type { SetManagedPostgresMaintenanceRequest } from '../models/SetManagedPostgresMaintenanceRequest.js';
import type { SetManagedPostgresQueryInsightsRequest } from '../models/SetManagedPostgresQueryInsightsRequest.js';
import type { SetManagedPostgresRecoveryRegionRequest } from '../models/SetManagedPostgresRecoveryRegionRequest.js';
import type { SetManagedPostgresSuspendTimeoutRequest } from '../models/SetManagedPostgresSuspendTimeoutRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ManagedPostgresService {
  protected static readonly clientConfig?: typeof OpenAPI;
  /**
   * Show plan and region PostgreSQL feature support
   * Returns configured provider-neutral support after plan limits without provider calls. provisioning_enabled includes qualification and canary gates. Current usage, budget, and quota admission are checked separately at reservation. Existing databases remain pinned to their original backend.
   * @returns ManagedPostgresCapabilities Effective regional capability contract
   * @returns Problem Authentication or capability lookup error
   * @throws ApiError
   */
  public static getManagedPostgresCapabilities({
    region,
  }: {
    /**
     * Portable region name; defaults to the configured region.
     */
    region?: string,
  }): CancelablePromise<ManagedPostgresCapabilities | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/capabilities',
      query: {
        'region': region,
      },
    });
  }
  /**
   * Show whether compute overage is on
   * With overage on, a paid account's managed PostgreSQL databases keep running past the plan's monthly compute allowance until the ceiling, twice the allowance, and are then suspended. Off is the default; the databases are then suspended at the allowance. A cap never deletes a database.
   * @returns ManagedPostgresOverage The account's overage setting and compute ceiling
   * @returns Problem Authentication, plan or capability error
   * @throws ApiError
   */
  public static getManagedPostgresOverage(): CancelablePromise<ManagedPostgresOverage | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/overage',
    });
  }
  /**
   * Turn compute overage on or off
   * Paid plans only; Free stops at its allowance and answers 403 managed_postgres_not_in_plan. Turning overage off while past the allowance suspends the databases at the next cap sweep. Compute above the allowance is not billed until managed PostgreSQL has invoicing.
   * @returns ManagedPostgresOverage The new overage setting and compute ceiling
   * @returns Problem Validation, authentication, plan or capability error
   * @throws ApiError
   */
  public static setManagedPostgresOverage({
    requestBody,
  }: {
    requestBody: ManagedPostgresOverageRequest,
  }): CancelablePromise<ManagedPostgresOverage | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/overage',
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * List managed PostgreSQL databases
   * @returns ManagedPostgresDatabaseList Account databases
   * @returns Problem Authentication or database listing error
   * @throws ApiError
   */
  public static listManagedPostgresDatabases(): CancelablePromise<ManagedPostgresDatabaseList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases',
    });
  }
  /**
   * Create a managed PostgreSQL database
   * @returns Problem Invalid request, plan limit, or provider unavailable
   * @returns ManagedPostgresDatabase Database accepted or ready
   * @throws ApiError
   */
  public static createManagedPostgresDatabase({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateManagedPostgresDatabaseRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresDatabase> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Set and remove a database's labels.
   * Preview (resource-labels capability). Sets and removes labels in one
   * atomic write and returns every label the database carries after it, with
   * the same rules, codes and audit event as PATCH /v1/apps/{slug}/labels.
   * Requires postgres:manage or admin scope and MFA. While the capability is on,
   * GET /v1/postgres/databases and GET /v1/postgres/databases/{id} include labels, and GET /v1/postgres/databases
   * takes ?selector=, a Kubernetes label selector.
   *
   * @returns LabelsResponse The database's labels after the write.
   * @returns Problem An invalid change, missing sign-in, scope or MFA, a database this account does not own, the capability turned off, or the rate limit.
   * @throws ApiError
   */
  public static updateManagedPostgresDatabaseLabels({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: LabelChange,
  }): CancelablePromise<LabelsResponse | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PATCH',
      url: '/v1/postgres/databases/{id}/labels',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Get managed PostgreSQL database status
   * @returns ManagedPostgresDatabase Database status without provider credentials
   * @returns Problem Authentication or database status error
   * @throws ApiError
   */
  public static getManagedPostgresDatabase({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresDatabase | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Delete a managed PostgreSQL database
   * A ready database is kept for 7 days with its compute stopped and its connections refused, and can be undeleted until restorable_until. It still counts toward the plan's database limit and keeps its name until it is purged. Any other database, or a delete with purge=true, is deleted at once. Bindings and restore dependencies must be removed first.
   * @returns ManagedPostgresDatabase Database deletion status
   * @returns Problem Authentication or database deletion error
   * @throws ApiError
   */
  public static deleteManagedPostgresDatabase({
    id,
    idempotencyKey,
    purge = false,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    /**
     * true deletes the database at once, including one already scheduled for deletion. It cannot be undone.
     */
    purge?: boolean,
  }): CancelablePromise<ManagedPostgresDatabase | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/postgres/databases/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'purge': purge,
      },
    });
  }
  /**
   * Read a managed database's conditions.
   * Preview (resource-conditions capability). The database's Ready,
   * Progressing and Degraded conditions, mapped from the response
   * GET /v1/postgres/databases/{id} returns, cached health included, and
   * never stored. Ready follows the lifecycle state; a database whose
   * compute scaled to zero is Ready=True with reason suspended. Failed,
   * deleting and deleted databases report terminal conditions.
   * Native databases include their storage allocation and last primary and
   * standby samples when available. A recent low-space or read-only sample
   * reports storage_low, storage_read_only or standby_storage_low on the
   * Degraded condition. Storage also includes recent primary alert transitions.
   * Reading this endpoint leaves parked databases asleep.
   * Cache-Control no-store.
   *
   * @returns ResourceStatus The managed database's conditions.
   * @returns Problem Missing sign-in, database read scope or MFA, a database this account does not own, managed databases not configured, the capability turned off, or the rate limit.
   * @throws ApiError
   */
  public static getManagedPostgresDatabaseStatus({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ResourceStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/status',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Run one read-only SQL statement.
   * Requires the data:read scope (or admin) and MFA.
   * Runs one statement as the database's agent
   * query login, a read-only login that reads the public schema, inside
   * a READ ONLY transaction with a 5 s statement timeout and a 1 s lock
   * timeout, then rolls back. Returns at most max_rows rows (default
   * 100, at most 1000) and 1 MiB of values, each in its PostgreSQL text
   * form or null; truncated is true when a cap stopped reading. One
   * query per database runs at a time. A suspended database wakes and
   * its compute is billed as usage. The SQL and rows are never logged;
   * the audit log records a SHA-256 of the statement. Cache-Control
   * no-store.
   *
   * @returns ManagedPostgresQueryResponse The rows read.
   * @returns Problem Missing sign-in, data:read scope or MFA, a database this account does not own, an invalid request, managed databases not configured, or the rate limit.
   * @throws ApiError
   */
  public static queryManagedPostgresDatabase({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresQueryRequest,
  }): CancelablePromise<ManagedPostgresQueryResponse | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/query',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        409: `The database is not ready, or another query of it is running.`,
        422: `PostgreSQL refused or failed the statement (postgres_query_failed); the detail carries its message and SQLSTATE.`,
        501: `The database's backend cannot run queries (postgres_query_unsupported).`,
      },
    });
  }
  /**
   * Undelete a managed PostgreSQL database
   * Returns a database whose delete is scheduled to ready, with its data, spec and restore history, while restorable_until is in the future. The plan must still allow the database's spec. After the window the database is gone and the request returns 404.
   * @returns ManagedPostgresDatabase Database returned to ready
   * @returns Problem Window ended, database not scheduled for deletion, plan limit, or provider error
   * @throws ApiError
   */
  public static undeleteManagedPostgresDatabase({
    id,
    idempotencyKey,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedPostgresDatabase | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/undelete',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    });
  }
  /**
   * Stage a managed PostgreSQL restore cutover
   * Requires managed PostgreSQL manage scope and a verified email. Stages all
   * source bindings for one app and scope on a ready restore target. Pins both
   * databases and the source bindings. Credentials remain unpublished;
   * workloads continue using the source. No activation is performed.
   *
   * @returns Problem Authentication, invalid restore target, conflict, or provider unavailable
   * @returns ManagedPostgresCutover Durable preparation status; Cache-Control no-store
   * @throws ApiError
   */
  public static prepareManagedPostgresCutover({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: PrepareManagedPostgresCutoverRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresCutover> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/cutovers',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read managed PostgreSQL cutover status
   * Requires managed PostgreSQL read scope. Verification freshness expires after five minutes and does not authorize activation.
   * @returns ManagedPostgresCutover Safe cutover metadata; Cache-Control no-store
   * @returns Problem Authentication or cutover status error
   * @throws ApiError
   */
  public static getManagedPostgresCutover({
    id,
  }: {
    /**
     * Gregale managed PostgreSQL cutover UUID.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresCutover | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/cutovers/{id}',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Verify a managed PostgreSQL cutover
   * Requires managed PostgreSQL manage scope. Queue read-only SQL authentication and ACL checks from the control plane against each staged target credential. Repeating after verification starts a fresh batch. This does not test application VM reachability or change bindings.
   * @returns Problem Authentication, state conflict, or cutover error
   * @returns ManagedPostgresCutover Durable verify status; Cache-Control no-store
   * @throws ApiError
   */
  public static verifyManagedPostgresCutover({
    id,
    idempotencyKey,
  }: {
    /**
     * Gregale managed PostgreSQL cutover UUID.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresCutover> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/cutovers/{id}/verify',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    });
  }
  /**
   * Queue a restore activation preflight
   * Requires managed PostgreSQL manage scope and verified email. Durably
   * queues catalog inspection, including while provider provisioning is
   * disabled. Concurrent pending requests keep the revision; requesting a
   * completed check starts a new revision. Does not fence admissions, call
   * a provider, publish bindings or resume workloads. Activation remains
   * disabled. Idempotency replay returns the original response; poll GET
   * for latest progress. Cancelling or cancelled cutovers conflict.
   *
   * @returns Problem Authentication, state conflict or cutover error
   * @returns ManagedPostgresCutoverPreflight Durable preflight request; Cache-Control no-store
   * @throws ApiError
   */
  public static requestManagedPostgresCutoverPreflight({
    id,
    idempotencyKey,
  }: {
    /**
     * Gregale managed PostgreSQL cutover UUID.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresCutoverPreflight> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/cutovers/{id}/preflight',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    });
  }
  /**
   * Read restore activation preflight findings
   * Requires managed PostgreSQL read scope. Findings describe a historical
   * catalog snapshot. Positive fields remain historical when stale is true.
   * Staleness is recomputed for expiry, cutover changes and cancellation.
   * Even fresh findings never authorize activation or prove writer isolation.
   * Returns 404 if no preflight has been requested.
   *
   * @returns ManagedPostgresCutoverPreflight Durable preflight findings; Cache-Control no-store
   * @returns Problem Authentication or preflight status error
   * @throws ApiError
   */
  public static getManagedPostgresCutoverPreflight({
    id,
  }: {
    /**
     * Gregale managed PostgreSQL cutover UUID.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresCutoverPreflight | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/cutovers/{id}/preflight',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Cancel a managed PostgreSQL cutover
   * Requires managed PostgreSQL manage scope. Queue revocation of all staged target credentials. Cleanup remains available with provisioning disabled. Source bindings and app secrets remain unchanged.
   * @returns Problem Authentication, unknown cutover, or cancellation conflict
   * @returns ManagedPostgresCutover Durable cancel status; Cache-Control no-store
   * @throws ApiError
   */
  public static cancelManagedPostgresCutover({
    id,
    idempotencyKey,
  }: {
    /**
     * Gregale managed PostgreSQL cutover UUID.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresCutover> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/cutovers/{id}/cancel',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    });
  }
  /**
   * Read managed PostgreSQL usage and guardrail state
   * Requires usage read scope. Returns normalized current-month meters,
   * plan headroom, and freshness state. Provider IDs, rates, credentials,
   * and internal cost line items are never returned.
   *
   * @returns ManagedPostgresUsageResponse Current managed PostgreSQL usage; Cache-Control no-store
   * @returns Problem Authentication or managed PostgreSQL accounting error
   * @throws ApiError
   */
  public static getManagedPostgresUsage(): CancelablePromise<ManagedPostgresUsageResponse | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/account/managed-postgres-usage',
    });
  }
  /**
   * Change managed PostgreSQL scale-to-zero policy
   * Durably reserves a scale-to-zero policy change on the pinned dataset. Clients may disconnect during the policy change. The request_id UUID is the durable idempotency key; repeat the same UUID and target to recover progress. Another resize, deletion, binding change, restore or cutover conflicts while updating. Existing accepted requests remain replayable after admission closes. Only compute idle policy changes; ready confirms provider observation, not uninterrupted connections. Published environment-clone targets and databases without a pinned data identity are currently unsupported.
   * @returns Problem Compute policy validation, concurrency, entitlement or provider admission failure
   * @returns ManagedPostgresComputePolicyChange Accepted policy change or current progress for an existing request
   * @throws ApiError
   */
  public static changeManagedPostgresComputePolicy({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ChangeManagedPostgresComputePolicyRequest,
  }): CancelablePromise<Problem | ManagedPostgresComputePolicyChange> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/compute-policy',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Get managed PostgreSQL compute policy progress
   * Reads a durable compute policy request for the authenticated account and database. Responses are not cached and omit private provider identity.
   * @returns ManagedPostgresComputePolicyChange Account and database scoped compute policy progress
   * @returns Problem Authentication or policy lookup error
   * @throws ApiError
   */
  public static getManagedPostgresComputePolicyChange({
    id,
    changeId,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Canonical UUID supplied as request_id when reserving this policy change.
     */
    changeId: string,
  }): CancelablePromise<ManagedPostgresComputePolicyChange | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/compute-policy-changes/{change_id}',
      path: {
        'id': id,
        'change_id': changeId,
      },
    });
  }
  /**
   * Set when a scale-to-zero database suspends
   * Sets how long a scale-to-zero database stays up after its last activity, from 60 to 3600 seconds, or with null returns it to the platform default. Every wake bills at least this much compute. A suspended database takes the value at once; a running one takes it the next time it suspends, without interrupting connections. Repeating the same value is harmless. seconds is required; null is the default. An always-on database answers 409 suspend_timeout_always_on; one with a change in progress answers a conflict. While operations are closed, only a shorter timeout is taken.
   * @returns ManagedPostgresSuspendTimeout The suspend timeout in force
   * @returns Problem Validation, conflict, entitlement or admission failure
   * @throws ApiError
   */
  public static setManagedPostgresSuspendTimeout({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresSuspendTimeoutRequest,
  }): CancelablePromise<ManagedPostgresSuspendTimeout | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/suspend-timeout',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Set a native database's automatic storage growth ceiling.
   * Single-node databases grow at an idle park or during their maintenance
   * window. A zero ceiling disables future automatic growth.
   *
   * @returns ManagedPostgresAutoGrowStatus The stored auto-grow policy.
   * @returns Problem Invalid policy, missing access or database, or unsupported backend or compute policy.
   * @throws ApiError
   */
  public static setManagedPostgresAutoGrow({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresAutoGrowRequest,
  }): CancelablePromise<ManagedPostgresAutoGrowStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/auto-grow',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Set a weekly UTC window for planned native PostgreSQL base refreshes.
   * Available for always-on native PostgreSQL databases. Production refreshes
   * verify the standby before a planned switchover. Clients can disconnect
   * during a refresh. A null window restores the default refresh policy.
   *
   * @returns ManagedPostgresMaintenanceStatus The stored schedule, current or next window, and latest refresh.
   * @returns Problem Invalid schedule, missing access or database, or unsupported backend or compute policy.
   * @throws ApiError
   */
  public static setManagedPostgresMaintenance({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresMaintenanceRequest,
  }): CancelablePromise<ManagedPostgresMaintenanceStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/maintenance-window',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Resize managed PostgreSQL compute
   * Durably reserves a service-class change on the pinned dataset. Clients may disconnect during resizing. The request_id UUID is the durable idempotency key; repeat the same UUID and target to recover progress. Another resize, deletion, binding change, restore or cutover conflicts while updating. Existing accepted requests remain replayable after admission closes. Only compute class changes; ready confirms provider observation, not uninterrupted connections. Published environment-clone targets and databases without a pinned data identity are currently unsupported.
   * @returns Problem Invalid request, conflict, plan limit or backend unavailable
   * @returns ManagedPostgresResize Accepted resize or current progress for an existing request
   * @throws ApiError
   */
  public static resizeManagedPostgresDatabase({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ResizeManagedPostgresDatabaseRequest,
  }): CancelablePromise<Problem | ManagedPostgresResize> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/resize',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Get managed PostgreSQL resize progress
   * Reads a durable resize request for the authenticated account and database. Responses are not cached and omit private provider identity.
   * @returns ManagedPostgresResize Account and database scoped resize progress
   * @returns Problem Authentication or resize lookup error
   * @throws ApiError
   */
  public static getManagedPostgresResize({
    id,
    resizeId,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Canonical UUID supplied as request_id when reserving this resize.
     */
    resizeId: string,
  }): CancelablePromise<ManagedPostgresResize | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/resizes/{resize_id}',
      path: {
        'id': id,
        'resize_id': resizeId,
      },
    });
  }
  /**
   * Check prerequisites for a native PostgreSQL live migration
   * Inspects source replication settings, capacity, supported table structures and an empty target. It may wake the target. The check creates no replication resources.
   * @returns ManagedPostgresMigrationCheck Compatibility evidence or explicit blockers.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static checkManagedPostgresMigration({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportSource,
  }): CancelablePromise<ManagedPostgresMigrationCheck | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/migration-check',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Start or resume a native PostgreSQL live migration
   * Copies tables into an empty single-zone native target and starts continuous logical replication with application connections fenced. Resume with the same operation ID and source credentials.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static startManagedPostgresMigration({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/migration',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Get native PostgreSQL live migration progress
   * Reports initial table copy and source WAL progress without waking a parked target.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresMigration({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/migration',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Stop a native PostgreSQL live migration
   * Stops applying changes and attempts to clean up verified source replication resources. The target remains fenced.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static stopManagedPostgresMigration({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/migration/stop',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Prepare the target of a native PostgreSQL live migration
   * Queues schema preparation after initial table copy. Builds supported indexes while the subscriber continues to apply changes and reports source schema blockers. Read progress from the migration status endpoint.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static prepareManagedPostgresMigration({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/migration/prepare',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Verify durable replay after a PostgreSQL migration source write pause
   * Queues a replicated marker and sequence synchronization after an operator confirms a source write pause. Read the receipt from migration status. Keep source writes paused throughout verification and until activation.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static verifyManagedPostgresMigration({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresMigrationVerifyRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/migration/verify',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a daily portable backup schedule and its last outcome.
   * @returns ManagedPostgresBackupSchedule The destination, next scheduled run and most recent outcome.
   * @returns Problem The schedule is absent or the caller cannot access it.
   * @throws ApiError
   */
  public static getManagedPostgresBackupSchedule({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBackupSchedule | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/backup-schedule',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Schedule a daily portable backup to a private Object Storage bucket.
   * Upsert one daily UTC schedule. The database and the ready, private destination bucket belong to the same owner scope. Requires PostgreSQL management and destination write authority. Transfers use the destination's single-upload limit.
   * @returns ManagedPostgresBackupSchedule The saved backup schedule.
   * @returns Problem Validation, permission, or active execution conflict.
   * @throws ApiError
   */
  public static setManagedPostgresBackupSchedule({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresBackupScheduleRequest,
  }): CancelablePromise<ManagedPostgresBackupSchedule | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/backup-schedule',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Disable future portable backup runs.
   * Leaves the objects already delivered to the bucket. An active delivery lease can return conflict.
   * @returns Problem Permission or active execution conflict.
   * @throws ApiError
   */
  public static deleteManagedPostgresBackupSchedule({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/postgres/databases/{id}/backup-schedule',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Start a portable export of a native PostgreSQL database
   * Produces a consistent custom-format PostgreSQL dump in the background. Requires verified PostgreSQL management access. The caller-supplied request ID identifies the same export on retry.
   * @returns ManagedPostgresExport The export already reached its terminal state.
   * @returns Problem Invalid request, access denied, unavailable export, or expired export.
   * @throws ApiError
   */
  public static startManagedPostgresExport({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresExportRequest,
  }): CancelablePromise<ManagedPostgresExport | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/exports',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL export and its manifest
   * The ready manifest carries the snapshot time, PostgreSQL major, extension versions, size and SHA-256 checksum.
   * @returns ManagedPostgresExport Export status.
   * @returns Problem Invalid request, access denied, unavailable export, or expired export.
   * @throws ApiError
   */
  public static getManagedPostgresExport({
    id,
    exportId,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    exportId: string,
  }): CancelablePromise<ManagedPostgresExport | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/exports/{export_id}',
      path: {
        'id': id,
        'export_id': exportId,
      },
    });
  }
  /**
   * Download a completed native PostgreSQL export
   * Streams the private PostgreSQL custom archive before its expiry. Authenticate with PostgreSQL management access.
   * @returns binary Standard PostgreSQL custom archive.
   * @returns Problem Invalid request, access denied, unavailable export, or expired export.
   * @throws ApiError
   */
  public static downloadManagedPostgresExport({
    id,
    exportId,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    exportId: string,
  }): CancelablePromise<Blob | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/exports/{export_id}/download',
      path: {
        'id': id,
        'export_id': exportId,
      },
    });
  }
  /**
   * Check a PostgreSQL source and an empty native import target
   * Checks the source connection, major version, extensions, target emptiness and estimated storage. May wake the target to inspect it. Requires PostgreSQL management access.
   * @returns ManagedPostgresImportStatus Import evidence.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static checkManagedPostgresImport({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportSource,
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/imports/check',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Start a snapshot import into an empty native PostgreSQL database
   * Copies a consistent source snapshot. Reuse the operation ID to recover or retry the same import. The copy leaves application bindings in place.
   * @returns ManagedPostgresImportStatus Import evidence.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static startManagedPostgresImport({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/imports',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Restore a Gregale-exported PostgreSQL archive to an empty native database
   * Uploads a PostgreSQL custom archive and its signed export manifest. Send the manifest part first. The target must be empty and compatible. Inspect the existing import status endpoint after upload.
   * @returns ManagedPostgresImportStatus Import progress or preflight failure.
   * @returns Problem Invalid receipt, invalid archive, access denied, conflicting operation, or unsupported target.
   * @throws ApiError
   */
  public static uploadManagedPostgresArchive({
    id,
    operationId,
    formData,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    operationId: string,
    formData: {
      manifest: ManagedPostgresExportManifest;
      archive: Blob;
    },
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/imports/{operation_id}/archive',
      path: {
        'id': id,
        'operation_id': operationId,
      },
      formData: { manifest: formData.manifest, archive: formData.archive },
      mediaType: 'multipart/form-data',
    });
  }
  /**
   * Read the progress of a native PostgreSQL import
   * Reads the import without waking the target. An unverified result means the current guest cannot report the requested operation.
   * @returns ManagedPostgresImportStatus Import evidence.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresImport({
    id,
    operationId,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    operationId: string,
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/imports/{operation_id}',
      path: {
        'id': id,
        'operation_id': operationId,
      },
    });
  }
  /**
   * List native PostgreSQL extension versions
   * Reads the current primary and may wake it. An optional later major compares installed versions with the configured upgrade image.
   * @returns ManagedPostgresExtensions Available managed versions and observed installed extensions.
   * @returns Problem Invalid request, access denied, or unsupported or unavailable database.
   * @throws ApiError
   */
  public static listManagedPostgresExtensions({
    id,
    targetMajor,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    targetMajor?: number,
  }): CancelablePromise<ManagedPostgresExtensions | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/extensions',
      path: {
        'id': id,
      },
      query: {
        'target_major': targetMajor,
      },
    });
  }
  /**
   * Enable a managed PostgreSQL extension
   * Installs the default version of the extension on the native primary. Repeating the operation preserves an installed version.
   * @returns ManagedPostgresExtensions Available managed versions and observed installed extensions.
   * @returns Problem Invalid request, access denied, or unsupported or unavailable database.
   * @throws ApiError
   */
  public static enableManagedPostgresExtension({
    id,
    extension,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    extension: 'btree_gist' | 'pg_trgm' | 'vector',
  }): CancelablePromise<ManagedPostgresExtensions | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/extensions/{extension}',
      path: {
        'id': id,
        'extension': extension,
      },
    });
  }
  /**
   * Inspect native PostgreSQL client sessions
   * Samples a running primary without waking a parked database. A cancel target identifies one currently tracked pooled query.
   * @returns ManagedPostgresSessions Session metadata and available cancel targets.
   * @returns Problem Access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static listManagedPostgresSessions({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresSessions | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/sessions',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Send a cancel request for a live PostgreSQL query target
   * Sends a PostgreSQL cancel request through the proxy that owns the execution. The target must come from a recent session sample. Replaying the same target returns its recorded outcome while the receipt is retained.
   * @returns ManagedPostgresQueryCancellation Cancel delivery outcome. Signal sent confirms sending the request to PostgreSQL.
   * @returns Problem Invalid target, access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static cancelManagedPostgresQuery({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: CancelManagedPostgresQueryRequest,
  }): CancelablePromise<ManagedPostgresQueryCancellation | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/cancel-query',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL autovacuum profile and effective settings
   * Reads the selected profile and live settings where available. A parked database stays asleep.
   * @returns ManagedPostgresAutovacuum Autovacuum profile status.
   * @returns Problem Access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresAutovacuum({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresAutovacuum | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/autovacuum',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Set a native PostgreSQL autovacuum profile
   * Saves the requested profile. Gregale reloads running instances and applies the saved profile at the next wake on parked databases.
   * @returns ManagedPostgresAutovacuum The saved profile and its current application status.
   * @returns Problem Invalid profile, access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static setManagedPostgresAutovacuum({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresAutovacuumRequest,
  }): CancelablePromise<ManagedPostgresAutovacuum | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/autovacuum',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read recent errors from native PostgreSQL pooled connections.
   * Returns newest first. The history keeps up to 1000 events per database for up to 24 hours. Reading history does not wake a parked database.
   * @returns ManagedPostgresErrorHistory Collection state and matching error metadata.
   * @returns Problem Invalid filter, permission, or database problem.
   * @throws ApiError
   */
  public static getManagedPostgresErrorHistory({
    id,
    sinceSeconds = 3600,
    sqlstate,
    limit = 40,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    sinceSeconds?: number,
    sqlstate?: string,
    limit?: number,
  }): CancelablePromise<ManagedPostgresErrorHistory | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/errors',
      path: {
        'id': id,
      },
      query: {
        'since_seconds': sinceSeconds,
        'sqlstate': sqlstate,
        'limit': limit,
      },
    });
  }
  /**
   * Enable or disable collection of pooled PostgreSQL errors.
   * Collection defaults to disabled. Disabling collection keeps events already retained until they expire.
   * @returns ManagedPostgresErrorHistory The saved collection state and recent events.
   * @returns Problem Invalid settings, permission, or database problem.
   * @throws ApiError
   */
  public static setManagedPostgresErrorHistory({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresErrorHistoryRequest,
  }): CancelablePromise<ManagedPostgresErrorHistory | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/errors',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Rank statements by execution time over an observed window
   * Reads stored numeric statement statistics for a single-zone native PostgreSQL database without waking it.
   * @returns ManagedPostgresQueryInsights Query statistics and observation coverage.
   * @returns Problem Invalid request, access denied, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresQueryInsights({
    id,
    sinceSeconds = 3600,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    sinceSeconds?: number,
  }): CancelablePromise<ManagedPostgresQueryInsights | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/query-insights',
      path: {
        'id': id,
      },
      query: {
        'since_seconds': sinceSeconds,
      },
    });
  }
  /**
   * Set native PostgreSQL query statistics collection
   * Applies the collection setting at an eligible primary restart. Historical observations remain available until they expire.
   * @returns ManagedPostgresQueryInsights Query statistics and observation coverage.
   * @returns Problem Invalid request, access denied, or unsupported backend.
   * @throws ApiError
   */
  public static setManagedPostgresQueryInsights({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresQueryInsightsRequest,
  }): CancelablePromise<ManagedPostgresQueryInsights | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/query-insights',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Diagnose a native PostgreSQL primary
   * Reads one bounded activity sample from the running primary. A parked database stays asleep and reports its state.
   * @returns ManagedPostgresDiagnostics Primary activity or its current sample availability.
   * @returns Problem Access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresDiagnostics({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresDiagnostics | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/diagnostics',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Inspect native PostgreSQL table health
   * Reads a bounded sample of stored tables and leaf partitions from the running primary. Parked databases stay asleep.
   * @returns ManagedPostgresTableHealth Table health or its current sample availability.
   * @returns Problem Invalid options, access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresTableHealth({
    id,
    sort = 'size',
    limit = 20,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Rank descending by total table and index size, estimated dead rows, or transaction ID age.
     */
    sort?: 'size' | 'dead-rows' | 'xid-age',
    limit?: number,
  }): CancelablePromise<ManagedPostgresTableHealth | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/tables',
      path: {
        'id': id,
      },
      query: {
        'sort': sort,
        'limit': limit,
      },
    });
  }
  /**
   * Queue a native PostgreSQL restore drill
   * Saves an idempotent request for a temporary restore and may wake the source to write a verification marker.
   * @returns Problem Invalid request, access denied, busy, or unsupported backend.
   * @returns ManagedPostgresRestoreDrill The recorded drill. Reusing the same request ID returns its current outcome.
   * @throws ApiError
   */
  public static startManagedPostgresRestoreDrill({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: RequestManagedPostgresRestoreDrill,
  }): CancelablePromise<Problem | ManagedPostgresRestoreDrill> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/restore-drills',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL restore drill
   * Reads a requested drill for the selected database. Reading the receipt leaves a parked source asleep.
   * @returns ManagedPostgresRestoreDrill The recorded drill and its progress.
   * @returns Problem Access denied, drill not found, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresRestoreDrill({
    id,
    drillId,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    drillId: string,
  }): CancelablePromise<ManagedPostgresRestoreDrill | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/restore-drills/{drill_id}',
      path: {
        'id': id,
        'drill_id': drillId,
      },
    });
  }
  /**
   * Queue a native PostgreSQL upgrade rehearsal
   * Queues a rehearsal of the target major on a private restore from verified archive evidence. The source keeps its current version and runtime state.
   * @returns Problem Invalid request, access denied, busy, or unsupported backend.
   * @returns ManagedPostgresUpgradeRehearsal The recorded rehearsal. Reuse the request ID with the same target major to read its current outcome.
   * @throws ApiError
   */
  public static startManagedPostgresUpgradeRehearsal({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: RequestManagedPostgresUpgradeRehearsal,
  }): CancelablePromise<Problem | ManagedPostgresUpgradeRehearsal> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/upgrade-rehearsals',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL upgrade rehearsal
   * Reads the rehearsal progress or outcome for the selected source database.
   * @returns ManagedPostgresUpgradeRehearsal The recorded rehearsal and its progress.
   * @returns Problem Access denied, rehearsal not found, or unsupported backend.
   * @throws ApiError
   */
  public static getManagedPostgresUpgradeRehearsal({
    id,
    rehearsalId,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    rehearsalId: string,
  }): CancelablePromise<ManagedPostgresUpgradeRehearsal | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/upgrade-rehearsals/{rehearsal_id}',
      path: {
        'id': id,
        'rehearsal_id': rehearsalId,
      },
    });
  }
  /**
   * Check native PostgreSQL archive evidence for a restore target.
   * Reads the selected base backup and bounded WAL evidence for this target. A covered result describes the archive at check time.
   * @returns ManagedPostgresRestoreCheck Archive evidence for the requested recovery point.
   * @returns Problem Invalid restore point, access denied, or unsupported backend.
   * @throws ApiError
   */
  public static checkManagedPostgresRestore({
    id,
    pointInTime,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    pointInTime: string,
  }): CancelablePromise<ManagedPostgresRestoreCheck | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/restore-check',
      path: {
        'id': id,
      },
      query: {
        'point_in_time': pointInTime,
      },
    });
  }
  /**
   * Create a masked branch of a native PostgreSQL database.
   * Copies selected data into a fresh database using the supplied masking policy. The target remains in provisioning until the copy and its constraints are validated. Requires managed PostgreSQL manage scope, MFA, and verified email.
   * @returns Problem Request refused or service unavailable.
   * @returns ManagedPostgresDatabase Masked branch accepted.
   * @throws ApiError
   */
  public static createManagedPostgresMaskedBranch({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: CreateManagedPostgresMaskedBranchRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresDatabase> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/masked-branches',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Set the recovery region for a native PostgreSQL database
   * @returns Problem Invalid region, insufficient access, or recovery unavailable.
   * @throws ApiError
   */
  public static setManagedPostgresRecoveryRegion({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresRecoveryRegionRequest,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/recovery-region',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Restore a database into a new managed PostgreSQL database
   * The restored database counts toward the plan's database limit. A restore may go one database past the limit: that copy reports restore_slot_until, 7 days out, and becomes an ordinary database once the account has room; otherwise its delete is scheduled then. A second restore past the limit while a copy holds the slot answers 403 managed_postgres_restore_slot_in_use.
   * @returns Problem Invalid point in time, plan limit, or provider error
   * @returns ManagedPostgresDatabase Restore accepted or ready
   * @throws ApiError
   */
  public static restoreManagedPostgresDatabase({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: RestoreManagedPostgresDatabaseRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresDatabase> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/restore',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Inspect read-only database connection planning
   * Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL, reserve connections or change admission. Current adapters have no verified usable-capacity observation and return unknown with canonical pool estimates. Database snapshots are independent.
   * @returns ManagedPostgresConnectionBudget Bounded credential-free planning and explicit unknown coverage
   * @returns Problem Authentication, ownership or metadata read error
   * @throws ApiError
   */
  public static getManagedPostgresConnectionBudget({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresConnectionBudget | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/connection-budget',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Inspect shared database budgets for Data API bindings
   * Lists independently sampled planning reports for the app's shared managed Data API databases, including other bindings in the same account. Requires managed PostgreSQL read permission. Reads metadata without waking SQL or changing admission; current adapters retain unknown capacity.
   * @returns DataAPIConnectionBudgets Per-database estimates and unknown coverage for this app's managed Data API bindings
   * @returns Problem Data API app lookup, permission or budget inventory failure
   * @throws ApiError
   */
  public static getAppDataApiConnectionBudgets({
    slug,
  }: {
    /**
     * App whose managed Data API bindings select the shared database reports.
     */
    slug: string,
  }): CancelablePromise<DataAPIConnectionBudgets | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/data-api/connection-budget',
      path: {
        'slug': slug,
      },
    });
  }
  /**
   * Download the current Data API runtime
   * Returns the Data API runtime this control plane serves: its version,
   * and a gzipped tar of the runtime under one `data-api/` directory with
   * its SHA-256. `gregale data-api create` and `gregale data-api deploy`
   * deploy this archive, so an older CLI release does not redeploy an older
   * runtime. A running Data API reports the version it was built from as
   * `runtime_version` on `/healthz`; a different version is not the
   * platform's current runtime.
   *
   * @returns DataAPIRuntime The current runtime
   * @returns Problem App lookup, permission or runtime failure
   * @throws ApiError
   */
  public static getDataApiRuntime({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<DataAPIRuntime | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/data-api/runtime',
      path: {
        'slug': slug,
      },
    });
  }
  /**
   * Read a Data API's platform status
   * Returns what the platform knows about the app's Data API without
   * waking it: the runtime version this control plane serves, whether
   * this installation accepts runtime reports, and the change hook
   * delivery the app's runtime last reported. Without runtime reports,
   * usage reports, monthly caps, keyless live reads and change hooks do
   * not reach the platform. `change_hooks` is absent until a runtime with
   * change hooks on has reported.
   *
   * @returns DataAPIStatus The Data API's status
   * @returns Problem App lookup, permission or status read failure
   * @throws ApiError
   */
  public static getDataApiStatus({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<DataAPIStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/data-api/status',
      path: {
        'slug': slug,
      },
    });
  }
  /**
   * Inspect a Data API app's database contract
   * Runs one fixed schema inspection inside a fresh task VM from the app's
   * live deployment: `types` renders the TypeScript contract, `validators`
   * the table and RPC input validators, and `schema` the JSON contract
   * snapshot. The receipt is an ordinary app task receipt and the output
   * arrives in `stdout_tail`. Belongs to the Data API, so it works without
   * the general app-task API.
   *
   * @returns AppTaskResponse Contract inspection admitted and queued.
   * @throws ApiError
   */
  public static createDataApiContractTask({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateDataAPIContractTaskRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/data-api/contract-tasks',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Get one Data API contract inspection
   * Current or terminal receipt of a contract inspection. Any other app task is not found here.
   * @returns AppTaskResponse Current or terminal contract inspection receipt.
   * @throws ApiError
   */
  public static getDataApiContractTask({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canonical UUID for a deployment-attached app task.
     */
    id: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/data-api/contract-tasks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Read Data API usage
   * Usage that the app's Data API runtime reported, per hour or UTC day, with month-to-date totals that owner caps compare against. Counts come from the app's own runtime and are owner telemetry, not billing records. Apps created before usage reporting, or with it off, have no rows.
   * @returns DataAPIUsage Zero-filled usage buckets, window totals and month-to-date totals
   * @returns Problem App lookup, window validation or usage storage failure
   * @throws ApiError
   */
  public static getAppDataApiUsage({
    slug,
    since,
    granularity,
  }: {
    /**
     * Data API app whose reported usage to read.
     */
    slug: string,
    /**
     * Lookback in whole hours or days, such as 24h, 7d or 30d (default 7d, at most 90d).
     */
    since?: string,
    /**
     * Bucket size; hour covers at most 48h. Defaults to hour up to 48h, else day.
     */
    granularity?: 'hour' | 'day',
  }): CancelablePromise<DataAPIUsage | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/data-api/usage',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'granularity': granularity,
      },
    });
  }
  /**
   * List an app's managed PostgreSQL bindings
   * Lists the app's live managed PostgreSQL bindings on every database it
   * is bound to, including organization databases and project environment
   * copies. Deleting the app is refused while a binding listed here is not
   * being deleted. Bindings on an environment copy are removed by tearing
   * down that environment.
   *
   * @returns AppPostgresBindingList The app's bindings on every database, without credential material
   * @returns Problem App lookup, permission or binding listing error
   * @throws ApiError
   */
  public static listAppPostgresBindings({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppPostgresBindingList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/postgres-bindings',
      path: {
        'slug': slug,
      },
    });
  }
  /**
   * List workload bindings for a database
   * @returns ManagedPostgresBindingList Bindings without credential material
   * @returns Problem Authentication or binding listing error
   * @throws ApiError
   */
  public static listManagedPostgresBindings({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBindingList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/bindings',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Bind a workload app to a database
   * The selected database backend must advertise the requested credential access. Unsupported access is rejected before a durable binding is reserved or provider credentials are requested.
   * @returns Problem Invalid request, conflict, or provider error
   * @returns ManagedPostgresBinding Binding accepted or ready; credentials are delivered through the app secret
   * @throws ApiError
   */
  public static createManagedPostgresBinding({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: CreateManagedPostgresBindingRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresBinding> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/bindings',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Get a workload database binding
   * @returns ManagedPostgresBinding Binding metadata without credential material
   * @returns Problem Authentication or binding status error
   * @throws ApiError
   */
  public static getManagedPostgresBinding({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBinding | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/bindings/{id}',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Remove a workload database binding
   * @returns ManagedPostgresBinding Binding deletion status
   * @returns Problem Authentication or binding deletion error
   * @throws ApiError
   */
  public static deleteManagedPostgresBinding({
    id,
    idempotencyKey,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedPostgresBinding | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/postgres/bindings/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    });
  }
  /**
   * Rotate a workload database credential
   * Creates a replacement credential, refreshes the app runtime, and retains the previous provider identity until the rolling refresh completes.
   * @returns ManagedPostgresBinding Binding credential generation and rotation status
   * @returns Problem Authentication or binding rotation error
   * @throws ApiError
   */
  public static rotateManagedPostgresBinding({
    id,
    idempotencyKey,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedPostgresBinding | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/bindings/{id}/rotate',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    });
  }
  /**
   * Get timeout defaults for a native pooled app binding.
   * @returns ManagedPostgresBindingTimeouts Saved values and the pool refresh state.
   * @returns Problem Problem reading binding timeout defaults.
   * @throws ApiError
   */
  public static getManagedPostgresBindingTimeouts({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBindingTimeouts | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/bindings/{id}/timeouts',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Save timeout defaults for a native pooled app binding.
   * Changes the supplied settings on a ready read_write or read_only binding. Zero disables a setting. Application happens asynchronously and existing transactions keep their current defaults.
   * @returns ManagedPostgresBindingTimeouts Saved values and the pool refresh state.
   * @returns Problem Invalid settings, conflict, or provider problem.
   * @throws ApiError
   */
  public static setManagedPostgresBindingTimeouts({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresBindingTimeoutsRequest,
  }): CancelablePromise<ManagedPostgresBindingTimeouts | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/bindings/{id}/timeouts',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Report resident Data API pool configuration
   * Private runtime surface. Requires a short-lived gregale:data-api:connections workload assertion for the live instance. Owner API keys and application JWTs are not accepted. Reports are reconciled against current catalog identity and expire within 30 seconds. This does not reserve connections or establish complete demand coverage.
   * @returns void
   * @throws ApiError
   */
  public static reportDataApiRuntimeConnections({
    requestBody,
  }: {
    requestBody: DataAPIRuntimeConnectionReport,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/data-api/connection-observations',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        409: `code: conflict`,
        422: `Observation reporting is unsupported by this catalog adapter.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Report Data API usage deltas
   * Private runtime surface. Requires a short-lived gregale:data-api:usage workload assertion from a live instance of an active app; account, app and instance come from it, never from the body. Only a production deployment reports; a stage, preview or cloned environment is refused for good with 403 workload_not_production, so its usage never counts against production's caps and its access settings never govern production's live reads. Each report id is applied once, so retried or snapshot-duplicated reports add nothing. Hours must be aligned and at most 48 hours old. Owner API keys and application JWTs are not accepted.
   * @returns DataAPIUsageReportResponse Report applied, or already applied; month-to-date totals for owner caps.
   * @throws ApiError
   */
  public static reportDataApiUsage({
    requestBody,
  }: {
    requestBody: DataAPIUsageReport,
  }): CancelablePromise<DataAPIUsageReportResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/data-api/usage',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Spend shared Data API request credit
   * Private runtime surface. Requires a dedicated gregale:data-api:budget workload assertion. Account and app come from the assertion. The body contains a digest of a verified subject or tenant and owner deployment policy. Admission spends one credit atomically across replicas; a lost reply may spend credit without customer SQL. No automatic replay is permitted.
   * @returns any Admission decision; a refusal does not dispatch customer SQL.
   * @throws ApiError
   */
  public static admitDataApiSharedBudget({
    requestBody,
  }: {
    requestBody: {
      version: 1;
      key: string;
      requests: number;
      window_seconds: number;
    },
  }): CancelablePromise<{
    allowed: boolean;
    retry_after_seconds: number;
  }> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/data-api/budget',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Publish recorded Data API row changes
   * Private runtime surface for change hooks. Requires a short-lived gregale:data-api:events workload assertion from a live instance of an active app; account and app come from it, never from the body. Only a production deployment publishes; a stage, preview or cloned environment is refused for good with 403 workload_not_production. Each event publishes as an app-scoped producer-key event under the app's own source with key data-api.change.ID, so a republished change deduplicates while its receipt is retained. Events publish in order; publication stops at the first event that may succeed later, and later events are reported as retryable without being attempted. Owner API keys and application JWTs are not accepted.
   * @returns DataAPIChangeEventBatchResponse Per-event outcomes in input order.
   * @throws ApiError
   */
  public static publishDataApiChangeEvents({
    requestBody,
  }: {
    requestBody: DataAPIChangeEventBatch,
  }): CancelablePromise<DataAPIChangeEventBatchResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/data-api/events',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        413: `The publication exceeds 1 MiB.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Report Data API change hook delivery
   * Private runtime surface for change hook delivery. Requires the same gregale:data-api:events workload assertion from a live instance of an active app as publication; account and app come from it, never from the body. The report carries counts the runtime read from its change event table (waiting and rejected changes, when the oldest waiting change was recorded, the latest rejection code, whether publications keep failing) and when it last published; it never carries change data. Owners read the latest report in the app's Data API status. Owner API keys and application JWTs are not accepted.
   * @returns void
   * @throws ApiError
   */
  public static reportDataApiChangeStatus({
    requestBody,
  }: {
    requestBody: DataAPIChangeHookReport,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/data-api/change-status',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List the organization's managed PostgreSQL databases.
   * Returns the company inventory across current and legacy storage namespaces. Current membership is required; personal databases are excluded.
   * @returns ManagedPostgresDatabaseList Result of the request to list the organization's managed PostgreSQL databases.
   * @returns Problem Problem while attempting to list the organization's managed PostgreSQL databases.
   * @throws ApiError
   */
  public static listOrgManagedPostgresDatabases({
    organization,
  }: {
    /**
     * Organization selected to list the organization's managed postgresql databases.
     */
    organization: string,
  }): CancelablePromise<ManagedPostgresDatabaseList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases',
      path: {
        'organization': organization,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Create a database owned by the organization.
   * An owner or admin reserves the database in the company namespace. Organization billing adoption, plan allowances and the shared database quota apply before provisioning.
   * @returns Problem Problem while attempting to create a database owned by the organization.
   * @returns ManagedPostgresDatabase Result of the request to create a database owned by the organization.
   * @throws ApiError
   */
  public static createOrgManagedPostgresDatabase({
    organization,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Organization selected to list the organization's managed postgresql databases.
     */
    organization: string,
    requestBody: CreateManagedPostgresDatabaseRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresDatabase> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases',
      path: {
        'organization': organization,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read PostgreSQL capabilities for the company plan.
   * Reports qualified provider capabilities intersected with the organization's plan. Reading capabilities does not open the provisioning rollout gate.
   * @returns ManagedPostgresCapabilities Result of the request to read PostgreSQL capabilities for the company plan.
   * @returns Problem Problem while attempting to read PostgreSQL capabilities for the company plan.
   * @throws ApiError
   */
  public static getOrgManagedPostgresCapabilities({
    organization,
    region,
  }: {
    /**
     * Organization selected to read postgresql capabilities for the company plan.
     */
    organization: string,
    /**
     * Placement region used to read postgresql capabilities for the company plan.
     */
    region?: string,
  }): CancelablePromise<ManagedPostgresCapabilities | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/capabilities',
      path: {
        'organization': organization,
      },
      query: {
        'region': region,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Inspect a company PostgreSQL database.
   * Reads the database under its persisted organization owner. Existing resource and provider identities remain stable after the creator leaves.
   * @returns ManagedPostgresDatabase Result of the request to inspect a company PostgreSQL database.
   * @returns Problem Problem while attempting to inspect a company PostgreSQL database.
   * @throws ApiError
   */
  public static getOrgManagedPostgresDatabase({
    organization,
    id,
  }: {
    /**
     * Organization selected to inspect a company postgresql database.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresDatabase | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}',
      path: {
        'organization': organization,
        'id': id,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Delete a company PostgreSQL resource.
   * An owner or admin requests provider cleanup. Active bindings and retained restore dependencies must be removed first; accounting evidence remains available. A ready database is kept for 7 days and can be undeleted until restorable_until; purge=true deletes at once.
   * @returns ManagedPostgresDatabase Result of the request to delete a company PostgreSQL resource.
   * @returns Problem Problem while attempting to delete a company PostgreSQL resource.
   * @throws ApiError
   */
  public static deleteOrgManagedPostgresDatabase({
    organization,
    id,
    idempotencyKey,
    purge = false,
  }: {
    /**
     * Organization selected to inspect a company postgresql database.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    /**
     * true deletes the company database at once, even during its undelete window. It cannot be undone.
     */
    purge?: boolean,
  }): CancelablePromise<ManagedPostgresDatabase | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/postgres/databases/{id}',
      path: {
        'organization': organization,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'purge': purge,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List the company database's app bindings.
   * Returns app and environment binding metadata for this organization-owned database. Connection credentials remain sealed and are not included in the response.
   * @returns ManagedPostgresBindingList Result of the request to list the company database's app bindings.
   * @returns Problem Problem while attempting to list the company database's app bindings.
   * @throws ApiError
   */
  public static listOrgManagedPostgresBindings({
    organization,
    id,
  }: {
    /**
     * Organization selected to list the company database's app bindings.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBindingList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/bindings',
      path: {
        'organization': organization,
        'id': id,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Attach the company database to an app environment.
   * The app and database must belong to the addressed company. Their storage namespaces may differ. Owners, admins and developers may attach bindings with the required database scope.
   * @returns Problem Problem while attempting to attach the company database to an app environment.
   * @returns ManagedPostgresBinding Result of the request to attach the company database to an app environment.
   * @throws ApiError
   */
  public static createOrgManagedPostgresBinding({
    organization,
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Organization selected to list the company database's app bindings.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: CreateManagedPostgresBindingRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresBinding> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/bindings',
      path: {
        'organization': organization,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Inspect a company database binding.
   * Resolves ownership through the binding's database before exposing metadata. Current organization membership is required even when the binding retains a creator namespace.
   * @returns ManagedPostgresBinding Result of the request to inspect a company database binding.
   * @returns Problem Problem while attempting to inspect a company database binding.
   * @throws ApiError
   */
  public static getOrgManagedPostgresBinding({
    organization,
    id,
  }: {
    /**
     * Organization selected to inspect a company database binding.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBinding | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/bindings/{id}',
      path: {
        'organization': organization,
        'id': id,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Detach a company database binding.
   * Revokes the managed provider credential and removes its sealed app-secret target. Other bindings on the same company database remain usable.
   * @returns ManagedPostgresBinding Result of the request to detach a company database binding.
   * @returns Problem Problem while attempting to detach a company database binding.
   * @throws ApiError
   */
  public static deleteOrgManagedPostgresBinding({
    organization,
    id,
    idempotencyKey,
  }: {
    /**
     * Organization selected to inspect a company database binding.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedPostgresBinding | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/postgres/bindings/{id}',
      path: {
        'organization': organization,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read a company database resize operation.
   * Returns progress for a durable resize UUID owned by this database and organization. Reading progress does not submit another resize.
   * @returns ManagedPostgresResize Result of the request to read a company database resize operation.
   * @returns Problem Problem while attempting to read a company database resize operation.
   * @throws ApiError
   */
  public static getOrgManagedPostgresResize({
    organization,
    id,
    resizeId,
  }: {
    /**
     * Organization selected to read a company database resize operation.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * UUID of the resize request whose company database progress is returned.
     */
    resizeId: string,
  }): CancelablePromise<ManagedPostgresResize | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/resizes/{resize_id}',
      path: {
        'organization': organization,
        'id': id,
        'resize_id': resizeId,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read a company database compute-policy operation.
   * Returns the durable policy change recorded for the addressed company database. The organization permission check also applies to old operation UUIDs.
   * @returns ManagedPostgresComputePolicyChange Result of the request to read a company database compute-policy operation.
   * @returns Problem Problem while attempting to read a company database compute-policy operation.
   * @throws ApiError
   */
  public static getOrgManagedPostgresComputePolicyChange({
    organization,
    id,
    changeId,
  }: {
    /**
     * Organization selected to read a company database compute-policy operation.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * UUID of the compute-policy change recorded for the company database.
     */
    changeId: string,
  }): CancelablePromise<ManagedPostgresComputePolicyChange | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/compute-policy-changes/{change_id}',
      path: {
        'organization': organization,
        'id': id,
        'change_id': changeId,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Undelete a company PostgreSQL database.
   * An owner or admin returns a company database whose delete is scheduled to ready while restorable_until is in the future. The company plan must still allow the database's spec.
   * @returns ManagedPostgresDatabase Result of the request to undelete a company PostgreSQL database.
   * @returns Problem Problem while attempting to undelete a company PostgreSQL database.
   * @throws ApiError
   */
  public static undeleteOrgManagedPostgresDatabase({
    organization,
    id,
    idempotencyKey,
  }: {
    /**
     * Organization that owns the company database to undelete.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedPostgresDatabase | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/undelete',
      path: {
        'organization': organization,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Check prerequisites for a native PostgreSQL live migration
   * Inspects source replication settings, capacity, supported table structures and an empty target. It may wake the target. The check creates no replication resources.
   * @returns ManagedPostgresMigrationCheck Compatibility evidence or explicit blockers.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static checkOrgManagedPostgresMigration({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportSource,
  }): CancelablePromise<ManagedPostgresMigrationCheck | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/migration-check',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Start or resume a native PostgreSQL live migration
   * Copies tables into an empty single-zone native target and starts continuous logical replication with application connections fenced. Resume with the same operation ID and source credentials.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static startOrgManagedPostgresMigration({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/migration',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Get native PostgreSQL live migration progress
   * Reports initial table copy and source WAL progress without waking a parked target.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresMigration({
    organization,
    id,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/migration',
      path: {
        'organization': organization,
        'id': id,
      },
    });
  }
  /**
   * Stop a native PostgreSQL live migration
   * Stops applying changes and attempts to clean up verified source replication resources. The target remains fenced.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static stopOrgManagedPostgresMigration({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/migration/stop',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Prepare the target of a native PostgreSQL live migration
   * Queues schema preparation after initial table copy. Builds supported indexes while the subscriber continues to apply changes and reports source schema blockers. Read progress from the migration status endpoint.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static prepareOrgManagedPostgresMigration({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/migration/prepare',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Verify durable replay after a PostgreSQL migration source write pause
   * Queues a replicated marker and sequence synchronization after an operator confirms a source write pause. Read the receipt from migration status. Keep source writes paused throughout verification and until activation.
   * @returns ManagedPostgresMigrationProgress Latest observed migration progress.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static verifyOrgManagedPostgresMigration({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresMigrationVerifyRequest,
  }): CancelablePromise<ManagedPostgresMigrationProgress | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/migration/verify',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a daily portable backup schedule and its last outcome.
   * @returns ManagedPostgresBackupSchedule The destination, next scheduled run and most recent outcome.
   * @returns Problem The schedule is absent or the caller cannot access it.
   * @throws ApiError
   */
  public static getOrgManagedPostgresBackupSchedule({
    organization,
    id,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBackupSchedule | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/backup-schedule',
      path: {
        'organization': organization,
        'id': id,
      },
    });
  }
  /**
   * Schedule a daily portable backup to a private Object Storage bucket.
   * Upsert one daily UTC schedule. The database and the ready, private destination bucket belong to the same owner scope. Requires PostgreSQL management and destination write authority. Transfers use the destination's single-upload limit.
   * @returns ManagedPostgresBackupSchedule The saved backup schedule.
   * @returns Problem Validation, permission, or active execution conflict.
   * @throws ApiError
   */
  public static setOrgManagedPostgresBackupSchedule({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresBackupScheduleRequest,
  }): CancelablePromise<ManagedPostgresBackupSchedule | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/backup-schedule',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Disable future portable backup runs.
   * Leaves the objects already delivered to the bucket. An active delivery lease can return conflict.
   * @returns Problem Permission or active execution conflict.
   * @throws ApiError
   */
  public static deleteOrgManagedPostgresBackupSchedule({
    organization,
    id,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/backup-schedule',
      path: {
        'organization': organization,
        'id': id,
      },
    });
  }
  /**
   * Start a portable export of a native PostgreSQL database
   * Produces a consistent custom-format PostgreSQL dump in the background. Requires verified PostgreSQL management access. The caller-supplied request ID identifies the same export on retry.
   * @returns ManagedPostgresExport The export already reached its terminal state.
   * @returns Problem Invalid request, access denied, unavailable export, or expired export.
   * @throws ApiError
   */
  public static startOrgManagedPostgresExport({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresExportRequest,
  }): CancelablePromise<ManagedPostgresExport | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/exports',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL export and its manifest
   * The ready manifest carries the snapshot time, PostgreSQL major, extension versions, size and SHA-256 checksum.
   * @returns ManagedPostgresExport Export status.
   * @returns Problem Invalid request, access denied, unavailable export, or expired export.
   * @throws ApiError
   */
  public static getOrgManagedPostgresExport({
    organization,
    id,
    exportId,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    exportId: string,
  }): CancelablePromise<ManagedPostgresExport | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/exports/{export_id}',
      path: {
        'organization': organization,
        'id': id,
        'export_id': exportId,
      },
    });
  }
  /**
   * Download a completed native PostgreSQL export
   * Streams the private PostgreSQL custom archive before its expiry. Authenticate with PostgreSQL management access.
   * @returns binary Standard PostgreSQL custom archive.
   * @returns Problem Invalid request, access denied, unavailable export, or expired export.
   * @throws ApiError
   */
  public static downloadOrgManagedPostgresExport({
    organization,
    id,
    exportId,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    exportId: string,
  }): CancelablePromise<Blob | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/exports/{export_id}/download',
      path: {
        'organization': organization,
        'id': id,
        'export_id': exportId,
      },
    });
  }
  /**
   * Check a PostgreSQL source and an empty native import target
   * Checks the source connection, major version, extensions, target emptiness and estimated storage. May wake the target to inspect it. Requires PostgreSQL management access.
   * @returns ManagedPostgresImportStatus Import evidence.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static checkOrgManagedPostgresImport({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportSource,
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/imports/check',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Start a snapshot import into an empty native PostgreSQL database
   * Copies a consistent source snapshot. Reuse the operation ID to recover or retry the same import. The copy leaves application bindings in place.
   * @returns ManagedPostgresImportStatus Import evidence.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static startOrgManagedPostgresImport({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ManagedPostgresImportRequest,
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/imports',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Restore a Gregale-exported PostgreSQL archive to an empty native database
   * Uploads a PostgreSQL custom archive and its signed export manifest. Send the manifest part first. The target must be empty and compatible. Inspect the existing import status endpoint after upload.
   * @returns ManagedPostgresImportStatus Import progress or preflight failure.
   * @returns Problem Invalid receipt, invalid archive, access denied, conflicting operation, or unsupported target.
   * @throws ApiError
   */
  public static uploadOrgManagedPostgresArchive({
    organization,
    id,
    operationId,
    formData,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    operationId: string,
    formData: {
      manifest: ManagedPostgresExportManifest;
      archive: Blob;
    },
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/imports/{operation_id}/archive',
      path: {
        'organization': organization,
        'id': id,
        'operation_id': operationId,
      },
      formData: { manifest: formData.manifest, archive: formData.archive },
      mediaType: 'multipart/form-data',
    });
  }
  /**
   * Read the progress of a native PostgreSQL import
   * Reads the import without waking the target. An unverified result means the current guest cannot report the requested operation.
   * @returns ManagedPostgresImportStatus Import evidence.
   * @returns Problem Invalid request, access denied, conflicting operation, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresImport({
    organization,
    id,
    operationId,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    operationId: string,
  }): CancelablePromise<ManagedPostgresImportStatus | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/imports/{operation_id}',
      path: {
        'organization': organization,
        'id': id,
        'operation_id': operationId,
      },
    });
  }
  /**
   * List native PostgreSQL extension versions
   * Reads the current primary and may wake it. An optional later major compares installed versions with the configured upgrade image.
   * @returns ManagedPostgresExtensions Available managed versions and observed installed extensions.
   * @returns Problem Invalid request, access denied, or unsupported or unavailable database.
   * @throws ApiError
   */
  public static listOrgManagedPostgresExtensions({
    organization,
    id,
    targetMajor,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    targetMajor?: number,
  }): CancelablePromise<ManagedPostgresExtensions | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/extensions',
      path: {
        'organization': organization,
        'id': id,
      },
      query: {
        'target_major': targetMajor,
      },
    });
  }
  /**
   * Enable a managed PostgreSQL extension
   * Installs the default version of the extension on the native primary. Repeating the operation preserves an installed version.
   * @returns ManagedPostgresExtensions Available managed versions and observed installed extensions.
   * @returns Problem Invalid request, access denied, or unsupported or unavailable database.
   * @throws ApiError
   */
  public static enableOrgManagedPostgresExtension({
    organization,
    id,
    extension,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    extension: 'btree_gist' | 'pg_trgm' | 'vector',
  }): CancelablePromise<ManagedPostgresExtensions | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/extensions/{extension}',
      path: {
        'organization': organization,
        'id': id,
        'extension': extension,
      },
    });
  }
  /**
   * Inspect native PostgreSQL client sessions
   * Samples a running primary without waking a parked database. A cancel target identifies one currently tracked pooled query.
   * @returns ManagedPostgresSessions Session metadata and available cancel targets.
   * @returns Problem Access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static listOrgManagedPostgresSessions({
    organization,
    id,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresSessions | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/sessions',
      path: {
        'organization': organization,
        'id': id,
      },
    });
  }
  /**
   * Send a cancel request for a live PostgreSQL query target
   * Sends a PostgreSQL cancel request through the proxy that owns the execution. The target must come from a recent session sample. Replaying the same target returns its recorded outcome while the receipt is retained.
   * @returns ManagedPostgresQueryCancellation Cancel delivery outcome. Signal sent confirms sending the request to PostgreSQL.
   * @returns Problem Invalid target, access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static cancelOrgManagedPostgresQuery({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: CancelManagedPostgresQueryRequest,
  }): CancelablePromise<ManagedPostgresQueryCancellation | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/cancel-query',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL autovacuum profile and effective settings
   * Reads the selected profile and live settings where available. A parked database stays asleep.
   * @returns ManagedPostgresAutovacuum Autovacuum profile status.
   * @returns Problem Access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresAutovacuum({
    organization,
    id,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresAutovacuum | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/autovacuum',
      path: {
        'organization': organization,
        'id': id,
      },
    });
  }
  /**
   * Set a native PostgreSQL autovacuum profile
   * Saves the requested profile. Gregale reloads running instances and applies the saved profile at the next wake on parked databases.
   * @returns ManagedPostgresAutovacuum The saved profile and its current application status.
   * @returns Problem Invalid profile, access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static setOrgManagedPostgresAutovacuum({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresAutovacuumRequest,
  }): CancelablePromise<ManagedPostgresAutovacuum | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/autovacuum',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read recent errors from native PostgreSQL pooled connections.
   * Returns newest first. The history keeps up to 1000 events per database for up to 24 hours. Reading history does not wake a parked database.
   * @returns ManagedPostgresErrorHistory Collection state and matching error metadata.
   * @returns Problem Invalid filter, permission, or database problem.
   * @throws ApiError
   */
  public static getOrgManagedPostgresErrorHistory({
    organization,
    id,
    sinceSeconds = 3600,
    sqlstate,
    limit = 40,
  }: {
    /**
     * Organization that owns the database.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    sinceSeconds?: number,
    sqlstate?: string,
    limit?: number,
  }): CancelablePromise<ManagedPostgresErrorHistory | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/errors',
      path: {
        'organization': organization,
        'id': id,
      },
      query: {
        'since_seconds': sinceSeconds,
        'sqlstate': sqlstate,
        'limit': limit,
      },
    });
  }
  /**
   * Enable or disable collection of pooled PostgreSQL errors.
   * Collection defaults to disabled. Disabling collection keeps events already retained until they expire.
   * @returns ManagedPostgresErrorHistory The saved collection state and recent events.
   * @returns Problem Invalid settings, permission, or database problem.
   * @throws ApiError
   */
  public static setOrgManagedPostgresErrorHistory({
    organization,
    id,
    requestBody,
  }: {
    /**
     * Organization that owns the database.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresErrorHistoryRequest,
  }): CancelablePromise<ManagedPostgresErrorHistory | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/errors',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Rank statements by execution time over an observed window
   * Reads stored numeric statement statistics for a single-zone native PostgreSQL database without waking it.
   * @returns ManagedPostgresQueryInsights Query statistics and observation coverage.
   * @returns Problem Invalid request, access denied, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresQueryInsights({
    organization,
    id,
    sinceSeconds = 3600,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    sinceSeconds?: number,
  }): CancelablePromise<ManagedPostgresQueryInsights | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/query-insights',
      path: {
        'organization': organization,
        'id': id,
      },
      query: {
        'since_seconds': sinceSeconds,
      },
    });
  }
  /**
   * Set native PostgreSQL query statistics collection
   * Applies the collection setting at an eligible primary restart. Historical observations remain available until they expire.
   * @returns ManagedPostgresQueryInsights Query statistics and observation coverage.
   * @returns Problem Invalid request, access denied, or unsupported backend.
   * @throws ApiError
   */
  public static setOrgManagedPostgresQueryInsights({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresQueryInsightsRequest,
  }): CancelablePromise<ManagedPostgresQueryInsights | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/query-insights',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Diagnose a native PostgreSQL primary
   * Reads one bounded activity sample from the running primary. A parked database stays asleep and reports its state.
   * @returns ManagedPostgresDiagnostics Primary activity or its current sample availability.
   * @returns Problem Access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresDiagnostics({
    organization,
    id,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresDiagnostics | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/diagnostics',
      path: {
        'organization': organization,
        'id': id,
      },
    });
  }
  /**
   * Inspect native PostgreSQL table health
   * Reads a bounded sample of stored tables and leaf partitions from the running primary. Parked databases stay asleep.
   * @returns ManagedPostgresTableHealth Table health or its current sample availability.
   * @returns Problem Invalid options, access denied, database not found, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresTableHealth({
    organization,
    id,
    sort = 'size',
    limit = 20,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Rank descending by total table and index size, estimated dead rows, or transaction ID age.
     */
    sort?: 'size' | 'dead-rows' | 'xid-age',
    limit?: number,
  }): CancelablePromise<ManagedPostgresTableHealth | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/tables',
      path: {
        'organization': organization,
        'id': id,
      },
      query: {
        'sort': sort,
        'limit': limit,
      },
    });
  }
  /**
   * Queue a native PostgreSQL restore drill
   * Saves an idempotent request for a temporary restore and may wake the source to write a verification marker.
   * @returns Problem Invalid request, access denied, busy, or unsupported backend.
   * @returns ManagedPostgresRestoreDrill The recorded drill. Reusing the same request ID returns its current outcome.
   * @throws ApiError
   */
  public static startOrgManagedPostgresRestoreDrill({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: RequestManagedPostgresRestoreDrill,
  }): CancelablePromise<Problem | ManagedPostgresRestoreDrill> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/restore-drills',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL restore drill
   * Reads a requested drill for the selected database. Reading the receipt leaves a parked source asleep.
   * @returns ManagedPostgresRestoreDrill The recorded drill and its progress.
   * @returns Problem Access denied, drill not found, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresRestoreDrill({
    organization,
    id,
    drillId,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    drillId: string,
  }): CancelablePromise<ManagedPostgresRestoreDrill | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/restore-drills/{drill_id}',
      path: {
        'organization': organization,
        'id': id,
        'drill_id': drillId,
      },
    });
  }
  /**
   * Queue a native PostgreSQL upgrade rehearsal
   * Queues a rehearsal of the target major on a private restore from verified archive evidence. The source keeps its current version and runtime state.
   * @returns Problem Invalid request, access denied, busy, or unsupported backend.
   * @returns ManagedPostgresUpgradeRehearsal The recorded rehearsal. Reuse the request ID with the same target major to read its current outcome.
   * @throws ApiError
   */
  public static startOrgManagedPostgresUpgradeRehearsal({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: RequestManagedPostgresUpgradeRehearsal,
  }): CancelablePromise<Problem | ManagedPostgresUpgradeRehearsal> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/upgrade-rehearsals',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a native PostgreSQL upgrade rehearsal
   * Reads the rehearsal progress or outcome for the selected source database.
   * @returns ManagedPostgresUpgradeRehearsal The recorded rehearsal and its progress.
   * @returns Problem Access denied, rehearsal not found, or unsupported backend.
   * @throws ApiError
   */
  public static getOrgManagedPostgresUpgradeRehearsal({
    organization,
    id,
    rehearsalId,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    rehearsalId: string,
  }): CancelablePromise<ManagedPostgresUpgradeRehearsal | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/upgrade-rehearsals/{rehearsal_id}',
      path: {
        'organization': organization,
        'id': id,
        'rehearsal_id': rehearsalId,
      },
    });
  }
  /**
   * Check native PostgreSQL archive evidence for a restore target.
   * Reads the selected base backup and bounded WAL evidence for this target. A covered result describes the archive at check time.
   * @returns ManagedPostgresRestoreCheck Archive evidence for the requested recovery point.
   * @returns Problem Invalid restore point, access denied, or unsupported backend.
   * @throws ApiError
   */
  public static checkOrgManagedPostgresRestore({
    organization,
    id,
    pointInTime,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    pointInTime: string,
  }): CancelablePromise<ManagedPostgresRestoreCheck | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/restore-check',
      path: {
        'organization': organization,
        'id': id,
      },
      query: {
        'point_in_time': pointInTime,
      },
    });
  }
  /**
   * Create a masked branch of a native PostgreSQL database.
   * Copies selected data into a fresh database using the supplied masking policy. The target remains in provisioning until the copy and its constraints are validated. Requires managed PostgreSQL manage scope, MFA, and verified email.
   * @returns Problem Request refused or service unavailable.
   * @returns ManagedPostgresDatabase Masked branch accepted.
   * @throws ApiError
   */
  public static createOrgManagedPostgresMaskedBranch({
    organization,
    id,
    requestBody,
    idempotencyKey,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: CreateManagedPostgresMaskedBranchRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresDatabase> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/masked-branches',
      path: {
        'organization': organization,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Set the recovery region for a native PostgreSQL database
   * @returns Problem Invalid region, insufficient access, or recovery unavailable.
   * @throws ApiError
   */
  public static setOrgManagedPostgresRecoveryRegion({
    organization,
    id,
    requestBody,
  }: {
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresRecoveryRegionRequest,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/recovery-region',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Restore the company database to a separate resource.
   * An owner or admin creates a restore within the source database's organization. The company plan, restore window and aggregate database reservation quota are enforced.
   * @returns Problem Problem while attempting to restore the company database to a separate resource.
   * @returns ManagedPostgresDatabase Result of the request to restore the company database to a separate resource.
   * @throws ApiError
   */
  public static restoreOrgManagedPostgresDatabase({
    organization,
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Organization selected to restore the company database to a separate resource.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: RestoreManagedPostgresDatabaseRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | ManagedPostgresDatabase> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/restore',
      path: {
        'organization': organization,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Change the company database service class.
   * An owner or admin submits a durable resize request. The requested class must fit the company plan and provider capabilities; connection interruption may occur.
   * @returns Problem Problem while attempting to change the company database service class.
   * @returns ManagedPostgresResize Result of the request to change the company database service class.
   * @throws ApiError
   */
  public static resizeOrgManagedPostgresDatabase({
    organization,
    id,
    requestBody,
  }: {
    /**
     * Organization selected to change the company database service class.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ResizeManagedPostgresDatabaseRequest,
  }): CancelablePromise<Problem | ManagedPostgresResize> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/resize',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Change the company database compute policy.
   * An owner or admin changes scale-to-zero policy with a durable request UUID. Current organization entitlements govern the request, including legacy databases.
   * @returns Problem Problem while attempting to change the company database compute policy.
   * @returns ManagedPostgresComputePolicyChange Result of the request to change the company database compute policy.
   * @throws ApiError
   */
  public static changeOrgManagedPostgresComputePolicy({
    organization,
    id,
    requestBody,
  }: {
    /**
     * Organization selected to change the company database compute policy.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: ChangeManagedPostgresComputePolicyRequest,
  }): CancelablePromise<Problem | ManagedPostgresComputePolicyChange> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/compute-policy',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Set when a company scale-to-zero database suspends.
   * An owner or admin sets how long a scale-to-zero company database stays up after its last activity, from 60 to 3600 seconds, or with null returns it to the platform default. A running database takes the value the next time it suspends.
   * @returns ManagedPostgresSuspendTimeout The company database's suspend timeout in force.
   * @returns Problem Problem while setting when the company database suspends.
   * @throws ApiError
   */
  public static setOrgManagedPostgresSuspendTimeout({
    organization,
    id,
    requestBody,
  }: {
    /**
     * Organization selected to set when a company database suspends.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresSuspendTimeoutRequest,
  }): CancelablePromise<ManagedPostgresSuspendTimeout | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/suspend-timeout',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Rotate a credential for a company app binding.
   * An owner or admin starts the existing credential refresh protocol. The previous generation stays valid until runtime delivery completes; the binding ID is preserved.
   * @returns ManagedPostgresBinding Result of the request to rotate a credential for a company app binding.
   * @returns Problem Problem while attempting to rotate a credential for a company app binding.
   * @throws ApiError
   */
  public static rotateOrgManagedPostgresBinding({
    organization,
    id,
    idempotencyKey,
  }: {
    /**
     * Organization selected to rotate a credential for a company app binding.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedPostgresBinding | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/postgres/bindings/{id}/rotate',
      path: {
        'organization': organization,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Get timeout defaults for a native pooled app binding.
   * @returns ManagedPostgresBindingTimeouts Saved values and the pool refresh state.
   * @returns Problem Problem reading binding timeout defaults.
   * @throws ApiError
   */
  public static getOrgManagedPostgresBindingTimeouts({
    organization,
    id,
  }: {
    /**
     * Organization that owns the app binding.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresBindingTimeouts | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/bindings/{id}/timeouts',
      path: {
        'organization': organization,
        'id': id,
      },
    });
  }
  /**
   * Save timeout defaults for a native pooled app binding.
   * Changes the supplied settings on a ready read_write or read_only binding. Zero disables a setting. Application happens asynchronously and existing transactions keep their current defaults.
   * @returns ManagedPostgresBindingTimeouts Saved values and the pool refresh state.
   * @returns Problem Invalid settings, conflict, or provider problem.
   * @throws ApiError
   */
  public static setOrgManagedPostgresBindingTimeouts({
    organization,
    id,
    requestBody,
  }: {
    /**
     * Organization that owns the app binding.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresBindingTimeoutsRequest,
  }): CancelablePromise<ManagedPostgresBindingTimeouts | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/bindings/{id}/timeouts',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read the organization's PostgreSQL usage totals.
   * Aggregates normalized usage across company database namespaces after billing adoption. Billing seats may read these totals without receiving database names or credentials.
   * @returns ManagedPostgresUsageResponse Result of the request to read the organization's PostgreSQL usage totals.
   * @returns Problem Problem while attempting to read the organization's PostgreSQL usage totals.
   * @throws ApiError
   */
  public static getOrgManagedPostgresUsage({
    organization,
  }: {
    /**
     * Organization selected to read the organization's postgresql usage totals.
     */
    organization: string,
  }): CancelablePromise<ManagedPostgresUsageResponse | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/postgres/usage',
      path: {
        'organization': organization,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
}
