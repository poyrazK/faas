/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded customer-authored JSON payload used only for isolated push/pull-worker or HTTP-function qualification. Do not include secrets.
 */
export type EnvironmentQueueSmoke = {
  /**
   * One JSON value, limited to 64 KiB, delivered to the candidate through its frozen queue invocation contract.
   */
  payload: (Record<string, any> | string | number | boolean | null);
};

