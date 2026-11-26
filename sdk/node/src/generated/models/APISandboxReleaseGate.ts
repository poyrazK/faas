/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type APISandboxReleaseGate = {
  /**
   * Current requirement status or the recorded decision after command admission.
   */
  status: 'waiting' | 'failed' | 'stale' | 'no_suite' | 'unavailable' | 'passed' | 'admitted';
  /**
   * The evaluated suite run or the run accepted at admission.
   */
  suite_run_id?: string;
};

