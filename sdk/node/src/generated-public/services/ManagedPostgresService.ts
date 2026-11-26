/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppPostgresBindingList } from '../models/AppPostgresBindingList.js';
import type { ChangeManagedPostgresComputePolicyRequest } from '../models/ChangeManagedPostgresComputePolicyRequest.js';
import type { CreateManagedPostgresBindingRequest } from '../models/CreateManagedPostgresBindingRequest.js';
import type { CreateManagedPostgresDatabaseRequest } from '../models/CreateManagedPostgresDatabaseRequest.js';
import type { ManagedPostgresBinding } from '../models/ManagedPostgresBinding.js';
import type { ManagedPostgresBindingList } from '../models/ManagedPostgresBindingList.js';
import type { ManagedPostgresCapabilities } from '../models/ManagedPostgresCapabilities.js';
import type { ManagedPostgresComputePolicyChange } from '../models/ManagedPostgresComputePolicyChange.js';
import type { ManagedPostgresConnectionBudget } from '../models/ManagedPostgresConnectionBudget.js';
import type { ManagedPostgresCustomRole } from '../models/ManagedPostgresCustomRole.js';
import type { ManagedPostgresCustomRoleList } from '../models/ManagedPostgresCustomRoleList.js';
import type { ManagedPostgresDatabase } from '../models/ManagedPostgresDatabase.js';
import type { ManagedPostgresDatabaseList } from '../models/ManagedPostgresDatabaseList.js';
import type { ManagedPostgresOverage } from '../models/ManagedPostgresOverage.js';
import type { ManagedPostgresOverageRequest } from '../models/ManagedPostgresOverageRequest.js';
import type { ManagedPostgresQueryRequest } from '../models/ManagedPostgresQueryRequest.js';
import type { ManagedPostgresQueryResponse } from '../models/ManagedPostgresQueryResponse.js';
import type { ManagedPostgresResize } from '../models/ManagedPostgresResize.js';
import type { ManagedPostgresSuspendTimeout } from '../models/ManagedPostgresSuspendTimeout.js';
import type { ManagedPostgresUsageResponse } from '../models/ManagedPostgresUsageResponse.js';
import type { ManagedPostgresWakeWithApp } from '../models/ManagedPostgresWakeWithApp.js';
import type { Problem } from '../models/Problem.js';
import type { PutManagedPostgresCustomRoleRequest } from '../models/PutManagedPostgresCustomRoleRequest.js';
import type { ResizeManagedPostgresDatabaseRequest } from '../models/ResizeManagedPostgresDatabaseRequest.js';
import type { RestoreManagedPostgresDatabaseRequest } from '../models/RestoreManagedPostgresDatabaseRequest.js';
import type { SetManagedPostgresSuspendTimeoutRequest } from '../models/SetManagedPostgresSuspendTimeoutRequest.js';
import type { SetManagedPostgresWakeWithAppRequest } from '../models/SetManagedPostgresWakeWithAppRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
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
   * List a database's custom roles.
   * Beta (postgres-custom-roles capability). The
   * NOLOGIN group roles declared on the database, with the access modes
   * whose logins may SET ROLE to each, the roles each is a member of, the
   * access modes whose logins start every session as it, and whether it
   * is applied (state ready) yet. Requires postgres read scope and MFA.
   *
   * @returns ManagedPostgresCustomRoleList The database's custom roles.
   * @returns Problem Missing sign-in, scope or MFA, a database this account does not own, managed databases not configured, the capability turned off, or the rate limit.
   * @throws ApiError
   */
  public static listManagedPostgresCustomRoles({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresCustomRoleList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/postgres/databases/{id}/roles',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Declare or replace a custom role.
   * Beta (postgres-custom-roles capability). Creates
   * the NOLOGIN role on the database, or corrects it to this declaration,
   * and grants it, SET only, to the database's logins of the granted
   * access modes, including logins issued later. The role and the
   * read_write logins can run the schema owner's functions in public.
   * bypass_rls is refused on a database with Data
   * API or Auth. Hobby and above; at most 16 roles per database. Requires
   * postgres manage scope, MFA and a verified email. Answers 200 when the
   * role is already applied and 202 while it is being applied.
   *
   * @returns ManagedPostgresCustomRole The role, applied.
   * @returns Problem An invalid declaration, a plan without custom roles, the role limit, a BYPASSRLS conflict with Data API or Auth, another default role for the access mode, a backend without custom roles, missing sign-in, scope, MFA or verified email, a database this account does not own, the capability turned off, or the rate limit.
   * @throws ApiError
   */
  public static putManagedPostgresCustomRole({
    id,
    name,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * The role name: a lowercase letter or underscore, then at most 62 lowercase letters, digits and underscores. Names starting with gregale, pg_, neon, cloudsql, rds, azure, supabase or cron_, and PostgreSQL's reserved role names, are refused.
     */
    name: string,
    requestBody: PutManagedPostgresCustomRoleRequest,
  }): CancelablePromise<ManagedPostgresCustomRole | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/roles/{name}',
      path: {
        'id': id,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Drop a custom role.
   * Beta (postgres-custom-roles capability). Marks the role deleting;
   * the platform then hands what it owns to the schema owner, removes its
   * grants and drops it. Requires postgres manage scope and MFA.
   *
   * @returns Problem An unknown role, missing sign-in, scope or MFA, a database this account does not own, the capability turned off, or the rate limit.
   * @returns ManagedPostgresCustomRole The role, deleting.
   * @throws ApiError
   */
  public static deleteManagedPostgresCustomRole({
    id,
    name,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    /**
     * The role name: a lowercase letter or underscore, then at most 62 lowercase letters, digits and underscores. Names starting with gregale, pg_, neon, cloudsql, rds, azure, supabase or cron_, and PostgreSQL's reserved role names, are refused.
     */
    name: string,
  }): CancelablePromise<Problem | ManagedPostgresCustomRole> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/postgres/databases/{id}/roles/{name}',
      path: {
        'id': id,
        'name': name,
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
   * Inspect public-schema metadata for a Data API connection.
   * Requires data:read (or admin) and MFA. Runs a fixed catalog-only
   * statement as the provider-managed read-only query identity, in a
   * bounded read-only transaction. No request body, customer table rows,
   * SQL expressions or credentials are returned. The query identity may
   * be created or refreshed and a suspended database can wake. Results
   * describe that identity, not a future Data API binding. At most 1000
   * rows and 1 MiB of values are returned; truncated marks partial results.
   * Cache-Control no-store.
   *
   * @returns ManagedPostgresQueryResponse Bounded public-schema catalog metadata.
   * @returns Problem Invalid request, unavailable database, insufficient access, unsupported backend or rate limit.
   * @throws ApiError
   */
  public static previewManagedPostgresDataApiCatalog({
    id,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
  }): CancelablePromise<ManagedPostgresQueryResponse | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/postgres/databases/{id}/data-api-catalog-preview',
      path: {
        'id': id,
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
   * Start a database when its app wakes
   * Turns on or off starting a scale-to-zero database when an app bound to it wakes, so the database's cold start overlaps the app's instead of following it. Off by default. A wake that never queries the database still starts it, and the compute bills at least one suspend timeout. Repeating the same value is harmless. An always-on database, or one on a backend that cannot start its compute, answers 409 wake_with_app_unsupported; one with a change in progress answers a conflict. While operations are closed, only turning it off is taken.
   * @returns ManagedPostgresWakeWithApp The setting in force
   * @returns Problem Invalid setting, unsupported database, conflict, entitlement or admission failure
   * @throws ApiError
   */
  public static setManagedPostgresWakeWithApp({
    id,
    requestBody,
  }: {
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresWakeWithAppRequest,
  }): CancelablePromise<ManagedPostgresWakeWithApp | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/postgres/databases/{id}/wake-with-app',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Start a company database when its app wakes.
   * An owner or admin turns on or off starting a scale-to-zero company database when an app bound to it wakes. A wake that never queries the database still starts it.
   * @returns ManagedPostgresWakeWithApp Whether the company database starts with its app.
   * @returns Problem Problem while setting whether the company database starts with its app.
   * @throws ApiError
   */
  public static setOrgManagedPostgresWakeWithApp({
    organization,
    id,
    requestBody,
  }: {
    /**
     * Organization selected to set whether a company database starts with its app.
     */
    organization: string,
    /**
     * Opaque Gregale managed PostgreSQL resource identifier.
     */
    id: string,
    requestBody: SetManagedPostgresWakeWithAppRequest,
  }): CancelablePromise<ManagedPostgresWakeWithApp | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/orgs/{organization}/postgres/databases/{id}/wake-with-app',
      path: {
        'organization': organization,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
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
