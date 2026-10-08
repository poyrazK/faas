/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Preview entity invocation; request_id and payload identify durable retries.
 */
export type DurableEntityInvokeRequest = {
  namespace: string;
  key: string;
  request_id: string;
  /**
   * JSON payload; replay fingerprints the exact decoded JSON bytes.
   */
  payload: any;
  /**
   * Registered project environment; defaults to production. Standalone apps use their default scope.
   */
  environment?: string;
  /**
   * Optional active customer owned by the authenticated account.
   */
  platform_tenant_id?: string;
};

