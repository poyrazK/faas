/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeUsageHour } from './RealtimeUsageHour.js';
/**
 * One endpoint's realtime usage in one UTC hour.
 */
export type RealtimeEndpointUsageHour = (RealtimeUsageHour & {
  /**
   * Realtime endpoint.
   */
  endpoint_id: string;
  /**
   * App that owns the endpoint.
   */
  app_id: string;
});

