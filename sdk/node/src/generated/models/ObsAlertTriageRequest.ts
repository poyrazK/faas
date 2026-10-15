/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One revision-checked triage action on an exact retained episode ID.
 */
export type ObsAlertTriageRequest = {
  action: 'acknowledge' | 'unacknowledge' | 'claim' | 'release' | 'snooze' | 'unsnooze';
  expected_revision: number;
  /**
   * Required only for snooze; strictly future and at most 24 hours ahead.
   */
  snoozed_until?: string;
};

