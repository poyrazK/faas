/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row per (event × target) emission. The dispatcher mutates
 * this row in place as attempts progress; the GET /deliveries
 * endpoint returns this shape per row. attempt=7 + status='dead'
 * is the DLQ state; the customer-facing retry endpoint flips a
 * dead row back to status='pending' for one more shot.
 *
 */
export type AppWebhookDeliveryResponse = {
  id: string;
  webhook_id: string;
  app_id: string;
  account_id: string;
  event: 'cron.fired' | 'cron.fired.manually' | 'app.created' | 'app.deleted' | 'app.deployed' | 'app.scaled' | 'app.parked' | 'app.woken' | 'build.succeeded' | 'build.failed' | 'deployment.failed' | 'rollout.aborted' | 'error.new' | 'job.finished' | 'preview.created' | 'budget.threshold';
  /**
   * The original event payload (omitted on rows past the first attempt; the customer has already seen it).
   */
  payload?: Record<string, any>;
  attempt: number;
  status: 'pending' | 'in_flight' | 'succeeded' | 'failed' | 'dead';
  last_error?: string;
  last_response_code?: number;
  next_attempt_at: string;
  delivered_at?: string;
  created_at: string;
  updated_at: string;
};

