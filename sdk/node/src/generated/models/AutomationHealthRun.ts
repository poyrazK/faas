/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe recent-run identity and timestamps; workflow input, output and error text are omitted.
 */
export type AutomationHealthRun = {
  id: string;
  status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead';
  created_at: string;
  finished_at?: string;
};

