/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One contained in-place pause after setup or at a cooperative managed-job checkpoint. No persistent VM snapshot is written.
 */
export type ScenarioSuspendResumeSpec = {
  version: 1;
  checkpoint: 'after_setup' | 'managed_job';
  /**
   * Required for managed_job; names one successful POST HTTP simulation fixture on the sole selected async worker.
   */
  worker_checkpoint?: string;
  workloads: Array<string>;
  pause_ms: number;
  /**
   * Maximum host-observed resume duration through hook and readiness.
   */
  within_ms: number;
};

