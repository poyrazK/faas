/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Versioned app.health.changed data. Explicit app-owned subscriptions
 * receive sampled status changes after a silent baseline. At most one
 * event per app every five minutes; pending changes combine to the latest
 * observed status, and returning to the last announced status cancels them.
 * Evidence gaps reset the comparison without inferring recovery. Unknown
 * is a confidence change, never a confirmed outage or recovery. The
 * transition points to retained history; evaluated_at describes the fresh
 * assessment used to queue this event. Delivery remains at least once.
 *
 */
export type AppHealthChangedWebhookPayload = {
  version: 1;
  app_id: string;
  scope: 'default';
  transition_id: string;
  transition_observed_at: string;
  /**
   * Last announced or silently established comparison status.
   */
  previous_status: 'healthy' | 'degraded' | 'unhealthy' | 'unknown';
  status: 'healthy' | 'degraded' | 'unhealthy' | 'unknown';
  change: 'worsened' | 'improved' | 'unconfirmed' | 'confirmed';
  phase: 'serving' | 'idle' | 'deploying' | 'not_deployed' | 'maintenance' | 'unsupported' | 'unknown';
  evaluated_at: string;
  queued_at: string;
  /**
   * Changes were combined during the notification cooldown.
   */
  coalesced: boolean;
  /**
   * Minimum spacing between app health notification intents.
   */
  cooldown_seconds: number;
  latest_deployment_id?: string;
  serving_deployment_ids: Array<string>;
  /**
   * Authenticated cursor-paged history; locate transition_id while retained.
   */
  history_path: string;
};

