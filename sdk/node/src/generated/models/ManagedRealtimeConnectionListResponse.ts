/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeConnectionResponse } from './ManagedRealtimeConnectionResponse.js';
/**
 * Bounded point-in-time live connection inventory.
 */
export type ManagedRealtimeConnectionListResponse = {
  connections: Array<ManagedRealtimeConnectionResponse>;
  limit: number;
  truncated: boolean;
  /**
   * True when one or more active realtime nodes did not answer.
   */
  partial: boolean;
  nodes_queried: number;
  nodes_unavailable: number;
};

