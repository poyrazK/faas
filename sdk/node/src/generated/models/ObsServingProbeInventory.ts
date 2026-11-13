/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ObsServingProbeInventory = {
  state: 'healthy' | 'stale' | 'unavailable';
  reason?: string;
  configured: boolean | null;
  expected_pairs: number | null;
  observed_at: string | null;
};

