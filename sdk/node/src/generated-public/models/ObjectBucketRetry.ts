/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Set while a bucket create or delete has failed and Gregale is retrying it. Reason is a bounded code, never a provider message.
 */
export type ObjectBucketRetry = {
  reason: 'temporary' | 'conflict' | 'configuration' | 'invalid' | 'protected' | 'cleanup_pending';
  /**
   * Customer-facing explanation of the reason.
   */
  detail: string;
  attempts: number;
  /**
   * When Gregale next tries the operation.
   */
  retry_at: string;
};

