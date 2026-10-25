/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Delayed task create/get/list shape with lifecycle and result metadata.
 */
export type DelayedTaskResponse = {
  id: string;
  app_id?: string;
  scheduled_at: string;
  state: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled' | 'dead_letter';
  method?: string;
  path?: string;
  attempts?: number;
  last_error?: string;
  result?: any;
  created_at?: string;
  completed_at?: string;
};

