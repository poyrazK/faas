/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable retained episode reference for recurrence investigation without raw source payloads or workflow identities.
 */
export type ObsAlertRecurrenceEpisode = {
  episode_id: string;
  status: 'firing' | 'resolved' | 'unknown';
  started_at: string;
  resolved_at: string | null;
  evidence_path: string;
};

