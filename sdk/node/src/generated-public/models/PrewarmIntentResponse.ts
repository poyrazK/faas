/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable scheduled/predicted prewarm intent and scheduler outcome.
 */
export type PrewarmIntentResponse = {
  id: string;
  app_id: string;
  count: number;
  wake_at: string;
  expires_at: string;
  trigger: 'calendar' | 'cron' | 'pattern' | 'webhook';
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  created_at: string;
  claimed_at?: string | null;
  fired_at?: string | null;
  /**
   * Instances actually admitted; may be lower than count when capacity gates apply.
   */
  admitted_count?: number;
  outcome?: string;
  last_error?: string;
};

