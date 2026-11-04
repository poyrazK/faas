/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Review digest must match current policy, configuration and deployment; caller-selected UUID permits create response recovery.
 */
export type CreateBindingVerificationRunRequest = {
  /**
   * Requested app receipt admission budget, from one to eight concurrent resources; defaults to two.
   */
  max_concurrency?: number;
  service_targets?: Record<string, string>;
  id: string;
  deployment_id: string;
  review_digest: string;
  ttl_seconds: number;
  object_storage_write: boolean;
  queue_publish: boolean;
};

