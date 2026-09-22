/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe durable invocation lifecycle projection linked to the trace.
 */
export type AccountTraceInvocation = {
  app: string;
  id: string;
  source: string;
  queue_name?: string;
  state: string;
  attempts: number;
  created_at: string;
  completed_at?: string;
  traceparent?: string;
};

