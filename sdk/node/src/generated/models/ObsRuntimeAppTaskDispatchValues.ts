/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit local dispatch and running bits and configured/occupied handler slots. Last claim success includes empty results; last sweep success includes empty sweeps. Both clocks are absent before success. They are not task completion or delivery acknowledgements.
 */
export type ObsRuntimeAppTaskDispatchValues = {
  enabled: boolean;
  running: boolean;
  slots_configured: number;
  slots_in_use: number;
  last_claim_success_at?: string;
  last_sweep_success_at?: string;
};

