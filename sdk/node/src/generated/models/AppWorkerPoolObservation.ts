/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkerAdmissionBlocker } from './WorkerAdmissionBlocker.js';
/**
 * Last scheduler target after environment scaling and cooldowns, before account or physical capacity admission. Counts describe that observation time; stale after fifteen minutes. Running counts only the target generation; predecessor and stopping residents count as draining.
 */
export type AppWorkerPoolObservation = {
  scope: string;
  deployment_id?: string;
  observed_at: string;
  stale: boolean;
  desired: number;
  running: number;
  starting: number;
  draining: number;
  /**
   * Last actual admission refusals for this pool. Absent evidence is unknown. A cooldown can retain earlier refusals with their original timestamps; these are not a fresh capacity forecast. Cleared when target slots are satisfied.
   */
  blockers?: Array<WorkerAdmissionBlocker>;
};

