/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial update of an existing webhook subscription. Every
 * field is optional — the handler merges the supplied fields
 * onto the current row. omit a field to leave it unchanged.
 *
 */
export type UpdateAppWebhookRequest = {
  target_url?: string;
  webhook_secret?: string;
  event_filter?: Array<'cron.fired' | 'cron.fired.manually' | 'app.created' | 'app.deleted' | 'app.deployed' | 'app.scaled' | 'app.parked' | 'app.woken' | 'build.succeeded' | 'build.failed' | 'deployment.failed' | 'rollout.aborted' | 'error.new' | 'job.finished' | 'preview.created' | 'budget.threshold'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  enabled?: boolean;
};

