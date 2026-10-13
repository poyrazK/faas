/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetryJobItem } from './EventExecutionLineageRetryJobItem.js';
export type EventExecutionLineageRetryJob = {
  request_id: string;
  subscription_id: string;
  app_id: string;
  status: 'pending' | 'completed';
  processed: number;
  items: Array<EventExecutionLineageRetryJobItem>;
  created_at: string;
  updated_at: string;
  retain_until: string;
};

