/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe workflow run metadata delivered after a run reaches succeeded, failed, or dead. Inputs, outputs, and error text are omitted; fetch the run using its ID when authorized.
 */
export type WorkflowFinishedWebhookPayload = {
  app_id: string;
  run_id: string;
  workflow_name: string;
  status: 'succeeded' | 'failed' | 'dead';
  finished_at: string;
  resume_count: number;
};

