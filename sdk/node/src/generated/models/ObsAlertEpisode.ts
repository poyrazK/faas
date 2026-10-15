/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable recurrence identity and retained observation clocks on a private operator incident.
 */
export type ObsAlertEpisode = {
  series_id: string;
  started_at: string;
  first_seen_at: string;
  last_seen_at: string;
  last_evaluated_at: string;
  resolved_at?: string;
  observation_state: 'current' | 'unknown' | 'historical';
};

