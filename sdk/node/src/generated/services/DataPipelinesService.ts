/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ControlDataPipelineRequest } from '../models/ControlDataPipelineRequest.js';
import type { CreateDataConnectionRequest } from '../models/CreateDataConnectionRequest.js';
import type { CreateDataPipelineRequest } from '../models/CreateDataPipelineRequest.js';
import type { DataConnection } from '../models/DataConnection.js';
import type { DataConnectionList } from '../models/DataConnectionList.js';
import type { DataPipeline } from '../models/DataPipeline.js';
import type { DataPipelineList } from '../models/DataPipelineList.js';
import type { Problem } from '../models/Problem.js';
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
}
