/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Whether a node can drain safely, and what blocks it.
 */
export type ObsNodeDrainStatus = {
  total_instances: number;
  live_instances: number;
  running_instances: number;
  waking_instances: number;
  cold_booting_instances: number;
  drain_safe: boolean;
  observed_at: string;
};

