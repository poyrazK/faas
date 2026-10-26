/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugRunningCause } from './DebugRunningCause.js';
import type { DebugRunningConfig } from './DebugRunningConfig.js';
import type { DebugRunningObservation } from './DebugRunningObservation.js';
/**
 * Customer-safe explanation of why an app remained resident. `current`
 * is the newest observation and `history` contains the bounded recent
 * observations in the requested window.
 *
 */
export type DebugRunningResponse = {
  app_id: string;
  since: string;
  window_start: string;
  window_end: string;
  retention_clamped: boolean;
  current: Array<DebugRunningCause>;
  current_observed_at?: string;
  config: DebugRunningConfig;
  history: Array<DebugRunningObservation>;
  history_truncated: boolean;
};

