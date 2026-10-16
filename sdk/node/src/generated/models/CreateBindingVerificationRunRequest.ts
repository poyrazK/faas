/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Review digest must match current policy, configuration and deployment; caller-selected UUID permits create response recovery.
 */
export type CreateBindingVerificationRunRequest = {
  /**
   * Frozen shared admission budget; omitted values use two. Graph members share the root budget.
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

