/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Subscribe a target URL to events emitted by the app. The
 * webhook_secret is HMAC-SHA256 sealed at rest with the host
 * X25519 recipient (namespace `APP_WEBHOOK`); apid mints a fresh
 * 32-byte secret if omitted.
 *
 */
export type CreateAppWebhookRequest = {
  target_url: string;
  webhook_secret: string;
  event_filter?: Array<'cron.fired' | 'cron.fired.manually' | 'app.created' | 'app.deleted' | 'app.deployed' | 'app.scaled' | 'app.parked' | 'app.woken' | 'build.succeeded' | 'build.failed' | 'deployment.failed' | 'rollout.aborted' | 'error.new' | 'job.finished' | 'preview.created' | 'budget.threshold'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  enabled?: boolean;
};

