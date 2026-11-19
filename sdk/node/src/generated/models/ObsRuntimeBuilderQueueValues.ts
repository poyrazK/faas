/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One notification queue's enabled flag, bounded counts and oldest waiting hint age; zero waiting omits the enqueue clock.
 */
export type ObsRuntimeBuilderQueueValues = {
  enabled: boolean;
  waiting: number;
  active: number;
  capacity: number;
  workers: number;
  oldest_enqueued_at?: string;
  oldest_wait_seconds: number;
};

