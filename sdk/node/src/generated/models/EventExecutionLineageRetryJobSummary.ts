/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventExecutionLineageRetryJobSummary = {
  request_id: string;
  subscription_id: string;
  app_id: string;
  status: 'pending' | 'paused' | 'completed' | 'cancelled';
  selected: number;
  /**
   * Cases evaluated or skipped; cancelled items are not counted.
   */
  processed: number;
  created_at: string;
  updated_at: string;
  retain_until: string;
};

