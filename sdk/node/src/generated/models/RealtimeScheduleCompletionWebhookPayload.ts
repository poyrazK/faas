/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Committed occurrence outcome delivered through app webhooks. Recurring publications and skips emit independently of the next occurrence's status; failures emit only when the retry budget is exhausted.
 */
export type RealtimeScheduleCompletionWebhookPayload = {
  event_id: string;
  app_id: string;
  endpoint_id: string;
  channel: string;
  schedule_id: string;
  version: number;
  occurrence: number;
  completed_occurrences: number;
  skipped_occurrences: number;
  outcome: 'published' | 'failed' | 'skipped';
  attempts: number;
  cycle_attempts: number;
  deliver_at: string;
  occurred_at: string;
  sequence?: number;
  failure_code?: string;
  skip_reason?: string;
};

