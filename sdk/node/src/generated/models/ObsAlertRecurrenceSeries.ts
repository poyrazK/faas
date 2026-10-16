/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertRecurrenceEpisode } from './ObsAlertRecurrenceEpisode.js';
import type { ObsAlertRecurrenceMetrics } from './ObsAlertRecurrenceMetrics.js';
/**
 * Exact rule/series recurrence group with safe resource identity and a bounded newest-first episode preview.
 */
export type ObsAlertRecurrenceSeries = {
  rule_key: string;
  series_id: string;
  name: string;
  severity: 'critical' | 'error' | 'warning';
  component: string;
  account_id?: string;
  app_id?: string;
  node_id?: string;
  first_activation_at: string;
  last_activation_at: string;
  metrics: ObsAlertRecurrenceMetrics;
  recent_episodes: Array<ObsAlertRecurrenceEpisode>;
  /**
   * Whether older cohort episodes of this series were omitted from the preview.
   */
  recent_episodes_truncated: boolean;
};

