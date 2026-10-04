/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class InternalService {
  /**
   * Internal — schedd posts a batch envelope to the gateway.
   * Internal-only route. Schedd invokes this once per closed
   * batch. The gateway delivers records sequentially and returns one
   * result per item. Each function response may include
   * `{"batchItemFailures":[{"itemIdentifier":"..."}]}`.
   * Durable queue records include an invocation ID and current claim
   * attempt; source must be esm for these records.
   *
   * @returns any Batch accepted; per-record status derived from response.
   * @throws ApiError
   */
  public static dispatchInvocationBatch({
    requestBody,
  }: {
    requestBody: {
      /**
       * Synthetic batch identity, typically trigger- followed by the trigger UUID.
       */
      invocation_id: string;
      trigger_id: string;
      app_id: string;
      /**
       * Internal dispatch source; esm for trigger and durable queue batches.
       */
      source?: string;
      records: Array<{
        item_identifier: string;
        /**
         * Internal durable queue invocation ID; must equal item_identifier and have a current claimed lease.
         */
        invocation_id?: string;
        /**
         * Required with invocation_id; fences delivery to the current durable queue claim attempt.
         */
        invocation_attempt?: number;
        payload_b64: string;
        headers?: Record<string, string>;
        metadata?: Record<string, any>;
      }>;
    },
  }): CancelablePromise<{
    results: Array<{
      item_identifier: string;
      status: 'succeeded' | 'retry' | 'dead_letter';
      error?: string;
      code?: string;
    }>;
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invocations:dispatch_batch',
      body: requestBody,
      mediaType: 'application/json',
    });
  }
}
