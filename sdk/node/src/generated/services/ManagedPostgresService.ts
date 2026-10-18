/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ChangeManagedPostgresComputePolicyRequest } from '../models/ChangeManagedPostgresComputePolicyRequest.js';
import type { CreateManagedPostgresBindingRequest } from '../models/CreateManagedPostgresBindingRequest.js';
import type { CreateManagedPostgresDatabaseRequest } from '../models/CreateManagedPostgresDatabaseRequest.js';
import type { DataAPIConnectionBudgets } from '../models/DataAPIConnectionBudgets.js';
import type { DataAPIRuntimeConnectionReport } from '../models/DataAPIRuntimeConnectionReport.js';
import type { ManagedPostgresBinding } from '../models/ManagedPostgresBinding.js';
import type { ManagedPostgresBindingList } from '../models/ManagedPostgresBindingList.js';
import type { ManagedPostgresCapabilities } from '../models/ManagedPostgresCapabilities.js';
import type { ManagedPostgresComputePolicyChange } from '../models/ManagedPostgresComputePolicyChange.js';
import type { ManagedPostgresConnectionBudget } from '../models/ManagedPostgresConnectionBudget.js';
import type { ManagedPostgresCutover } from '../models/ManagedPostgresCutover.js';
import type { ManagedPostgresCutoverPreflight } from '../models/ManagedPostgresCutoverPreflight.js';
import type { ManagedPostgresDatabase } from '../models/ManagedPostgresDatabase.js';
import type { ManagedPostgresDatabaseList } from '../models/ManagedPostgresDatabaseList.js';
import type { ManagedPostgresResize } from '../models/ManagedPostgresResize.js';
import type { ManagedPostgresUsageResponse } from '../models/ManagedPostgresUsageResponse.js';
import type { PrepareManagedPostgresCutoverRequest } from '../models/PrepareManagedPostgresCutoverRequest.js';
import type { Problem } from '../models/Problem.js';
import type { ResizeManagedPostgresDatabaseRequest } from '../models/ResizeManagedPostgresDatabaseRequest.js';
import type { RestoreManagedPostgresDatabaseRequest } from '../models/RestoreManagedPostgresDatabaseRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ManagedPostgresService {
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
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/capabilities',
      query: {
        'region': region,
      },
    });
  }
  /**
   * List managed PostgreSQL databases
   * @returns ManagedPostgresDatabaseList Account databases
   * @returns Problem Authentication or database listing error
   * @throws ApiError
   */
  public static listManagedPostgresDatabases(): CancelablePromise<ManagedPostgresDatabaseList | Problem> {
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Delete a managed PostgreSQL database
   * @returns ManagedPostgresDatabase Database deletion status
   * @returns Problem Authentication or database deletion error
   * @throws ApiError
   */
  public static deleteManagedPostgresDatabase({
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
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/postgres/databases/{id}',
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/compute-policy-changes/{change_id}',
      path: {
        'id': id,
        'change_id': changeId,
      },
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/resizes/{resize_id}',
      path: {
        'id': id,
        'resize_id': resizeId,
      },
    });
  }
  /**
   * Restore a database into a new managed PostgreSQL database
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/data-api/connection-budget',
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/data-api/connection-observations',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        409: `code: conflict`,
        422: `Observation reporting is unsupported by this catalog adapter.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
