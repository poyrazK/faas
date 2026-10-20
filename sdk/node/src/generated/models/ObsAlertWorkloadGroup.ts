/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Nonempty retained-alert bucket for one exact status and workflow combination.
 */
export type ObsAlertWorkloadGroup = {
  status: 'firing' | 'resolved' | 'unknown';
  severity: 'critical' | 'error' | 'warning';
  acknowledged: boolean;
  owned: boolean;
  snoozed: boolean;
  count: number;
};

