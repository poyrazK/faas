/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sanitized post-action workflow flags from a durable receipt without actor or owner identities.
 */
export type ObsAlertTriageHistoryEntry = {
  id: string;
  revision: number;
  action: 'acknowledge' | 'unacknowledge' | 'claim' | 'release' | 'snooze' | 'unsnooze';
  at: string;
  acknowledged: boolean;
  owned: boolean;
  snoozed_until?: string;
  trace_id?: string;
  trace_path?: string;
};

