/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account webhook payload emitted when a disposable Run reaches a terminal status.
 */
export type ExecutionFinishedWebhookPayload = {
  event_id: string;
  execution_id: string;
  status: string;
  runtime: string;
  finished_at: string;
  receipt_url: string;
  environment_id?: string;
  workflow_id?: string;
  result_validation_status?: string;
};

