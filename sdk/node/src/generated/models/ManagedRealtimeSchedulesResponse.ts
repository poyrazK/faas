/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeScheduleResponse } from './ManagedRealtimeScheduleResponse.js';
import type { ManagedRealtimeScheduleTotals } from './ManagedRealtimeScheduleTotals.js';
/**
 * Matching retained channel schedules and their aggregate lifecycle totals.
 */
export type ManagedRealtimeSchedulesResponse = {
  totals: ManagedRealtimeScheduleTotals;
  schedules: Array<ManagedRealtimeScheduleResponse>;
};

