/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventExecutionLineageRetryJobControlReceipt = {
  command_id: string;
  sequence: number;
  job_request_id: string;
  subscription_id: string;
  app_id: string;
  action: 'pause' | 'resume';
  expected_created_at: string;
  /**
   * Immutable job state observed by the original command.
   */
  result_status: 'pending' | 'paused';
  processed: number;
  selected: number;
  job_updated_at: string;
  recorded_at: string;
  retain_until: string;
};

