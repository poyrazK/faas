/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Retention Request.
 */
export type RealtimeConsumerGroupRetentionRequest = {
  /**
   * Zero disables cleanup; otherwise 3600 to 2592000. Ordered groups require skip.
   */
  retention_seconds: number;
};

