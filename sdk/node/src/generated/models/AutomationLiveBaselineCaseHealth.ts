/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current retained verification availability and renewal readiness for one promoted suite case.
 */
export type AutomationLiveBaselineCaseHealth = {
  name: string;
  run_id?: string;
  status: 'passed' | 'failed' | 'pending' | 'unavailable' | 'not_started';
};

