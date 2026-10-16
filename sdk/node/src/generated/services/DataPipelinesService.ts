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
   * List registered data sources without credentials
   * @returns DataConnectionList List registered data sources without credentials
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @throws ApiError
   */
  public static listDataConnections(): CancelablePromise<DataConnectionList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/connections',
    });
  }
  /**
   * Register an encrypted PostgreSQL source
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @returns DataConnection Register an encrypted PostgreSQL source
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
   * Rotate credentials without changing source identity
   * @returns DataConnection Rotate credentials without changing source identity
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @throws ApiError
   */
  public static rotateDataConnection({
    id,
    requestBody,
  }: {
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
   * Delete a source after its pipelines are cleaned up
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @throws ApiError
   */
  public static deleteDataConnection({
    id,
  }: {
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
   * List managed data pipelines
   * @returns DataPipelineList List managed data pipelines
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @throws ApiError
   */
  public static listDataPipelines(): CancelablePromise<DataPipelineList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/data/pipelines',
    });
  }
  /**
   * Request initial copying and continuous PostgreSQL replication
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @returns DataPipeline Request initial copying and continuous PostgreSQL replication
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
   * Read pipeline intent and observed progress
   * @returns DataPipeline Read pipeline intent and observed progress
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @throws ApiError
   */
  public static getDataPipeline({
    id,
  }: {
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
   * Request a revision-checked pipeline action
   * @returns Problem Authentication, ownership, validation, quota or pipeline availability error
   * @returns DataPipeline Request a revision-checked pipeline action
   * @throws ApiError
   */
  public static controlDataPipeline({
    id,
    requestBody,
  }: {
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
