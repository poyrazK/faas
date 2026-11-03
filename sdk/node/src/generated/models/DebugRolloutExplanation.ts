/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugRegressionItem } from './DebugRegressionItem.js';
import type { DebugRolloutRoute } from './DebugRolloutRoute.js';
import type { DebugTelemetryRequestItem } from './DebugTelemetryRequestItem.js';
import type { RolloutDecision } from './RolloutDecision.js';
/**
 * Why a release was rolled back or is held, with the request evidence
 * behind the safe-release decision.
 *
 */
export type DebugRolloutExplanation = {
  deployment_id?: string;
  stable_deployment_id?: string;
  state: 'aborted' | 'held' | 'rolling_out' | 'complete' | 'none';
  traffic_percent: number;
  decision?: RolloutDecision;
  /**
   * Reason recorded by an abort that has no safe-release decision (route monitor, alert rollback, profile gate).
   */
  aborted_reason?: string;
  /**
   * Deterministic one-line explanation.
   */
  headline: string;
  window_start?: string;
  window_end?: string;
  retention_clamped?: boolean;
  routes: Array<DebugRolloutRoute>;
  examples: Array<DebugTelemetryRequestItem>;
  regressions: Array<DebugRegressionItem>;
  generated_at: string;
};

