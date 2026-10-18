/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable lifecycle metadata only. started_at is the latest claim, not the first attempt; no headers, traceparent, queue name, payload or result.
 */
export type SelectedTraceInvocation = {
  app_id: string;
  id: string;
  source: string;
  state: string;
  attempts: number;
  created_at: string;
  started_at?: string;
  completed_at?: string;
};

