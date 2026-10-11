/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Diagnostic notification sample. Overdue requires a known unacknowledged receiver and a capture timestamp at least fifteen minutes old. Missing receiver history does not prove delivery failure.
 */
export type EventRecoveryNotificationJobHealth = {
  job_id: string;
  kind: 'admission' | 'execution';
  event?: string;
  capture_status: string;
  acknowledgement_status: string;
  evidence_source: string;
  captured_at?: string;
  unacknowledged_age_seconds?: number;
  overdue: boolean;
  dead: boolean;
  unknown: boolean;
  no_receivers: boolean;
};

