/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeEndpointUsageHour } from './RealtimeEndpointUsageHour.js';
import type { RealtimeUsageHour } from './RealtimeUsageHour.js';
/**
 * An account's realtime usage against its fair-use limits.
 */
export type RealtimeUsageResponse = {
  /**
   * The account's plan.
   */
  plan: string;
  /**
   * Concurrent connections the plan allows per account; zero when the plan has no realtime.
   */
  connection_limit: number;
  /**
   * Connections open now on nodes that reported in the last 30 seconds.
   */
  open_connections: number;
  /**
   * Start of the first hour returned.
   */
  from: string;
  /**
   * End of the last hour returned (exclusive).
   */
  to: string;
  /**
   * Account usage per hour, oldest first.
   */
  hours: Array<RealtimeUsageHour>;
  /**
   * Usage per endpoint and hour, oldest first.
   */
  endpoints: Array<RealtimeEndpointUsageHour>;
};

