/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account webhook metadata for execution.pipeline.finished. Receivers fetch the authorized workflow receipt for the complete manifest. Uses existing signed delivery, retries, dead-letter and redrive.
 */
export type ExecutionPipelineFinishedWebhookPayload = {
  event_id: string;
  workflow_id: string;
  outcome: 'succeeded' | 'failed' | 'cancelled' | 'deadline_exceeded';
  finished_at: string;
  receipt_url: string;
};

