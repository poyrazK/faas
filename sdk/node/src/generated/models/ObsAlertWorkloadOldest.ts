/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sanitized oldest eligible episode reference ordered by activation time then immutable ID.
 */
export type ObsAlertWorkloadOldest = {
  incident_id: string;
  status: 'firing' | 'unknown';
  severity: 'critical' | 'error' | 'warning';
  started_at: string;
  age_seconds: number;
  evidence_path: string;
};

