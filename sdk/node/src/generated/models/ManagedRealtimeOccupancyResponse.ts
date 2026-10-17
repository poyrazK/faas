/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeChannelOccupancy } from './ManagedRealtimeChannelOccupancy.js';
/**
 * Point-in-time channel occupancy across the realtime fleet.
 */
export type ManagedRealtimeOccupancyResponse = {
  channels: Array<ManagedRealtimeChannelOccupancy>;
  /**
   * True when some active realtime nodes did not answer, so counts may be low.
   */
  partial: boolean;
  nodes_queried: number;
  nodes_unavailable: number;
};

