/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeScheduleHistoryEvent } from './ManagedRealtimeScheduleHistoryEvent.js';
export type ManagedRealtimeScheduleHistoryResponse = {
  schedule_id: string;
  channel: string;
  oldest_version: number;
  latest_version: number;
  history_truncated: boolean;
  has_more: boolean;
  events: Array<ManagedRealtimeScheduleHistoryEvent>;
};

