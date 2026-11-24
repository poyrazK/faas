/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SavedInsightRequest } from './SavedInsightRequest.js';
/**
 * A saved insight and its last scheduled run. Its value is an events total, a funnel's overall conversion or the first-period retention rate.
 */
export type SavedInsight = (SavedInsightRequest & {
  /**
   * The alert's state after the last successful scheduled run.
   */
  alert_state: 'ok' | 'firing';
  last_value?: number;
  last_run_at?: string;
  last_error?: string;
  next_digest_at?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
});

