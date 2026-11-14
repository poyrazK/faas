/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ControlDataPipelineRequest } from '../models/ControlDataPipelineRequest.js';
import type { CreateDataConnectionRequest } from '../models/CreateDataConnectionRequest.js';
import type { CreateDataPipelineRequest } from '../models/CreateDataPipelineRequest.js';
import type { DataConnection } from '../models/DataConnection.js';
import type { DataConnectionCheck } from '../models/DataConnectionCheck.js';
import type { DataConnectionList } from '../models/DataConnectionList.js';
import type { DataPipeline } from '../models/DataPipeline.js';
import type { DataPipelineEvolutionRequest } from '../models/DataPipelineEvolutionRequest.js';
import type { DataPipelineFreshnessRequest } from '../models/DataPipelineFreshnessRequest.js';
import type { DataPipelineIndexRequest } from '../models/DataPipelineIndexRequest.js';
import type { DataPipelineList } from '../models/DataPipelineList.js';
import type { DataPipelineOperationsRequest } from '../models/DataPipelineOperationsRequest.js';
import type { DataPipelineSchemaApprovalRequest } from '../models/DataPipelineSchemaApprovalRequest.js';
import type { DataPipelineSetup } from '../models/DataPipelineSetup.js';
import type { DataPipelineSetupControlRequest } from '../models/DataPipelineSetupControlRequest.js';
import type { DataPipelineSetupList } from '../models/DataPipelineSetupList.js';
import type { DataPipelineSetupPlan } from '../models/DataPipelineSetupPlan.js';
import type { DataPipelineSetupRequest } from '../models/DataPipelineSetupRequest.js';
import type { DataPipelineTenantReadersRequest } from '../models/DataPipelineTenantReadersRequest.js';
import type { Problem } from '../models/Problem.js';
import type { RequestDataConnectionCheck } from '../models/RequestDataConnectionCheck.js';
import type { RotateDataConnectionRequest } from '../models/RotateDataConnectionRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DataPipelinesService {
  /**
   * List sealed PostgreSQL pipeline source identities
   * @returns DataConnectionList Account-owned source identities with credentials omitted.
   * @returns Problem Problem returned by listDataConnections after account, credential or pipeline validation.
   * @throws ApiError
   */
  public static listDataConnections(): CancelablePromise<DataConnectionList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/connections',
    });
  }
  /**
   * Seal a credential for a PostgreSQL pipeline source
   * @returns Problem Problem returned by createDataConnection after account, credential or pipeline validation.
   * @returns DataConnection Registered source identity and its initial credential revision.
   * @throws ApiError
   */
  public static createDataConnection({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateDataConnectionRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | DataConnection> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/connections',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Replace the credential of a PostgreSQL pipeline source
   * @returns DataConnection Source identity with an incremented credential revision.
   * @returns Problem Problem returned by rotateDataConnection after account, credential or pipeline validation.
   * @throws ApiError
   */
  public static rotateDataConnection({
    id,
    requestBody,
  }: {
    /**
     * Sealed source connection to rotate or remove.
     */
    id: string,
    requestBody: RotateDataConnectionRequest,
  }): CancelablePromise<DataConnection | Problem> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/data/connections/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Remove an unused PostgreSQL pipeline source
   * @returns Problem Problem returned by deleteDataConnection after account, credential or pipeline validation.
   * @throws ApiError
   */
  public static deleteDataConnection({
    id,
  }: {
    /**
     * Sealed source connection to rotate or remove.
     */
    id: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/data/connections/{id}',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Queue a private worker connection check and table discovery
   * Staging only. Coalesces active or very recent checks for the same credential revision and optional mirror. No source SQL runs in the HTTP handler. Results are compatibility observations, not a production qualification.
   * @returns Problem Problem returned by requestDataConnectionCheck after account, revision or target validation.
   * @returns DataConnectionCheck Accepted or coalesced durable connection check.
   * @throws ApiError
   */
  public static requestDataConnectionCheck({
    id,
    requestBody,
  }: {
    /**
     * Account-owned sealed source connection.
     */
    id: string,
    requestBody: RequestDataConnectionCheck,
  }): CancelablePromise<Problem | DataConnectionCheck> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/connections/{id}/check',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read the latest connection check and discovered source schema
   * Account-scoped, bounded metadata only. A credential rotation makes earlier evidence stale; completed checks expire after 90 seconds. No source credentials or customer rows are returned.
   * @returns DataConnectionCheck Latest check, freshness, compatibility issues and discovered tables.
   * @returns Problem Problem returned by getDataConnectionCheck after account or connection validation.
   * @throws ApiError
   */
  public static getDataConnectionCheck({
    id,
  }: {
    /**
     * Account-owned sealed source connection.
     */
    id: string,
  }): CancelablePromise<DataConnectionCheck | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/connections/{id}/check',
      path: {
        'id': id,
      },
    });
  }
  /**
   * List managed PostgreSQL table mirrors
   * @returns DataPipelineList Bounded account-owned pipeline intent and observation history.
   * @returns Problem Problem returned by listDataPipelines after account, credential or pipeline validation.
   * @throws ApiError
   */
  public static listDataPipelines(): CancelablePromise<DataPipelineList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/pipelines',
    });
  }
  /**
   * Start a managed PostgreSQL table mirror
   * @returns Problem Problem returned by createDataPipeline after account, credential or pipeline validation.
   * @returns DataPipeline Durable replication intent accepted for asynchronous reconciliation.
   * @throws ApiError
   */
  public static createDataPipeline({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateDataPipelineRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read synchronization progress for a PostgreSQL table mirror
   * @returns DataPipeline Current desired action and the most recent replication observation.
   * @returns Problem Problem returned by getDataPipeline after account, credential or pipeline validation.
   * @throws ApiError
   */
  public static getDataPipeline({
    id,
  }: {
    /**
     * Managed table mirror whose progress to inspect.
     */
    id: string,
  }): CancelablePromise<DataPipeline | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/pipelines/{id}',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Change managed PostgreSQL table mirror intent
   * @returns Problem Problem returned by controlDataPipeline after account, credential or pipeline validation.
   * @returns DataPipeline Updated intent revision accepted for worker reconciliation.
   * @throws ApiError
   */
  public static controlDataPipeline({
    id,
    requestBody,
  }: {
    /**
     * Managed table mirror receiving the lifecycle request.
     */
    id: string,
    requestBody: ControlDataPipelineRequest,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines/{id}/control',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Configure scheduled pipeline work
   * Set bounded snapshot schedules, curated quality rules and UTC work budgets, or request the next snapshot or quality run. Import mode and serving selection remain immutable. Manual requests respect work windows.
   * @returns Problem Scheduled operations validation, ownership, admission or intent conflict.
   * @returns DataPipeline Scheduled work policy or explicit run request accepted.
   * @throws ApiError
   */
  public static configureDataPipelineOperations({
    id,
    requestBody,
  }: {
    /**
     * Pipeline whose scheduled maintenance policy is updated.
     */
    id: string,
    requestBody: DataPipelineOperationsRequest,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines/{id}/operations',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Preview or approve pipeline serving evolution
   * Save an additive projection, Auth organization mapping, or declared relationship preview. Approval supplies only its saved ID and revision. Projection changes subsequently require the exact source schema or table approval before copying and exposure.
   * @returns Problem Evolution validation, ownership, admission or revision conflict.
   * @returns DataPipeline Serving evolution preview saved, discarded or approved for reconciliation.
   * @throws ApiError
   */
  public static evolveDataPipeline({
    id,
    requestBody,
  }: {
    /**
     * Pipeline whose serving evolution is reviewed.
     */
    id: string,
    requestBody: DataPipelineEvolutionRequest,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines/{id}/evolution',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Approve the exact pipeline source schema
   * Approve the saved schema_review.id after inspecting its exact column and table changes. Retry reuses the previously approved change and preserves bounded backfill and verification receipts.
   * @returns Problem Source-schema approval validation, ownership, freshness or revision conflict.
   * @returns DataPipeline Exact source-definition approval saved for verified pipeline evolution.
   * @throws ApiError
   */
  public static approveDataPipelineSchema({
    id,
    requestBody,
  }: {
    /**
     * Pipeline whose worker-generated source schema review is approved.
     */
    id: string,
    requestBody: DataPipelineSchemaApprovalRequest,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines/{id}/schema-approval',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Preview or approve an exact tenant membership change
   * @returns Problem Problem returned by changeDataPipelineTenantReaders after account, credential or pipeline validation.
   * @returns DataPipeline Tenant membership preview saved or approved for atomic mirror policy reconciliation.
   * @throws ApiError
   */
  public static changeDataPipelineTenantReaders({
    id,
    requestBody,
  }: {
    /**
     * Pipeline whose tenant membership preview or approval is being managed.
     */
    id: string,
    requestBody: DataPipelineTenantReadersRequest,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines/{id}/tenant-readers',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Preview durable setup resources and billing
   * @returns DataPipelineSetupPlan Preview durable setup resources and billing response.
   * @returns Problem Validation or availability problem for planDataPipelineSetup.
   * @throws ApiError
   */
  public static planDataPipelineSetup({
    requestBody,
  }: {
    requestBody: DataPipelineSetupRequest,
  }): CancelablePromise<DataPipelineSetupPlan | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipeline-setups/plan',
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Save a resumable mirror and Data API setup
   * @returns Problem Validation or availability problem for createDataPipelineSetup.
   * @returns DataPipelineSetup Save a resumable mirror and Data API setup response.
   * @throws ApiError
   */
  public static createDataPipelineSetup({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: DataPipelineSetupRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<Problem | DataPipelineSetup> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipeline-setups',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * List saved pipeline setup jobs
   * @returns DataPipelineSetupList List saved pipeline setup jobs response.
   * @returns Problem Validation or availability problem for listDataPipelineSetups.
   * @throws ApiError
   */
  public static listDataPipelineSetups(): CancelablePromise<DataPipelineSetupList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/pipeline-setups',
    });
  }
  /**
   * Inspect durable pipeline setup progress
   * @returns DataPipelineSetup Inspect durable pipeline setup progress response.
   * @returns Problem Validation or availability problem for getDataPipelineSetup.
   * @throws ApiError
   */
  public static getDataPipelineSetup({
    id,
  }: {
    /**
     * Owned resource receiving getDataPipelineSetup requests.
     */
    id: string,
  }): CancelablePromise<DataPipelineSetup | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/pipeline-setups/{id}',
      path: {
        'id': id,
      },
    });
  }
  /**
   * Retry or cancel a saved pipeline setup
   * @returns Problem Validation or availability problem for controlDataPipelineSetup.
   * @returns DataPipelineSetup Retry or cancel a saved pipeline setup response.
   * @throws ApiError
   */
  public static controlDataPipelineSetup({
    id,
    requestBody,
  }: {
    /**
     * Owned resource receiving controlDataPipelineSetup requests.
     */
    id: string,
    requestBody: DataPipelineSetupControlRequest,
  }): CancelablePromise<Problem | DataPipelineSetup> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipeline-setups/{id}/control',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Configure checkpoint freshness confirmation
   * @returns Problem Validation or availability problem for configureDataPipelineFreshness.
   * @returns DataPipeline Configure checkpoint freshness confirmation response.
   * @throws ApiError
   */
  public static configureDataPipelineFreshness({
    id,
    requestBody,
  }: {
    /**
     * Owned resource receiving configureDataPipelineFreshness requests.
     */
    id: string,
    requestBody: DataPipelineFreshnessRequest,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines/{id}/freshness',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Preview and approve bounded mirror indexes
   * @returns Problem Validation or availability problem for changeDataPipelineIndexes.
   * @returns DataPipeline Preview and approve bounded mirror indexes response.
   * @throws ApiError
   */
  public static changeDataPipelineIndexes({
    id,
    requestBody,
  }: {
    /**
     * Owned resource receiving changeDataPipelineIndexes requests.
     */
    id: string,
    requestBody: DataPipelineIndexRequest,
  }): CancelablePromise<Problem | DataPipeline> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/data/pipelines/{id}/indexes',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
}
