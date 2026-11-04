/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugRunningCause } from './DebugRunningCause.js';
/**
 * Bounded point-in-time scheduler observation for the running debugger.
 */
export type DebugRunningObservation = {
  event_id?: string;
  observed_at: string;
  running_instances: number;
  configured_min_instances: number;
  effective_min_instances: number;
  prewarm_min_instances?: number;
  idle_timeout_seconds: number;
  /**
   * True when one or more scheduler signals were unavailable.
   */
  degraded?: boolean;
  causes: Array<DebugRunningCause>;
};

