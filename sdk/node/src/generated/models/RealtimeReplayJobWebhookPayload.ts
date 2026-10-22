/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Payload of realtime.replay_job.completed, realtime.replay_job.cancelled and realtime.replay_job.processing_failed app webhooks. Use event_id to deduplicate delivery retries. Counts describe the committed transition; cancellation does not undo earlier replays.
 */
export type RealtimeReplayJobWebhookPayload = {
  event_id: string;
  app_id: string;
  endpoint_id: string;
  group_id: string;
  job_id: string;
  channel: string;
  group: string;
  state: 'running' | 'completed' | 'cancelled';
  rate: number;
  selected: number;
  processed: number;
  replayed: number;
  skipped: number;
  remaining: number;
  error_count: number;
  /**
   * processing_failed on the warning event; empty for terminal events. Contains no raw database error.
   */
  error_code: string;
  /**
   * One warning per job when cumulative processing errors first reach this threshold.
   */
  error_threshold: number;
  /**
   * Relative inspector link on the Gregale console origin. Recent job history may later be pruned.
   */
  inspector_path: string;
  occurred_at: string;
};

