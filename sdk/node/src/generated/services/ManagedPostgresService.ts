/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateManagedPostgresBindingRequest } from '../models/CreateManagedPostgresBindingRequest.js';
import type { CreateManagedPostgresDatabaseRequest } from '../models/CreateManagedPostgresDatabaseRequest.js';
import type { ManagedPostgresBinding } from '../models/ManagedPostgresBinding.js';
import type { ManagedPostgresBindingList } from '../models/ManagedPostgresBindingList.js';
import type { ManagedPostgresCapabilities } from '../models/ManagedPostgresCapabilities.js';
import type { ManagedPostgresCutover } from '../models/ManagedPostgresCutover.js';
import type { ManagedPostgresDatabase } from '../models/ManagedPostgresDatabase.js';
import type { ManagedPostgresDatabaseList } from '../models/ManagedPostgresDatabaseList.js';
import type { ManagedPostgresUsageResponse } from '../models/ManagedPostgresUsageResponse.js';
import type { PrepareManagedPostgresCutoverRequest } from '../models/PrepareManagedPostgresCutoverRequest.js';
import type { Problem } from '../models/Problem.js';
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
}
