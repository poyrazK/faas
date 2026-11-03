/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugRegressionItem } from './DebugRegressionItem.js';
import type { DebugRolloutExplanation } from './DebugRolloutExplanation.js';
import type { DebugStatusDeployment } from './DebugStatusDeployment.js';
import type { DebugStatusHealth } from './DebugStatusHealth.js';
import type { DebugStatusOOMKill } from './DebugStatusOOMKill.js';
import type { DebugTelemetryRequestItem } from './DebugTelemetryRequestItem.js';
/**
 * One summary of an app's health, release, regressions, and recent failures over `since`.
 */
export type DebugStatusResponse = {
  verdict: 'failing' | 'rolled_back' | 'degraded' | 'releasing' | 'healthy' | 'idle';
  headline: string;
  since: string;
  window_start: string;
  window_end: string;
  health: DebugStatusHealth;
  deployments: Array<DebugStatusDeployment>;
  rollout?: DebugRolloutExplanation;
  regressions: Array<DebugRegressionItem>;
  regressions_total: number;
  recent_failures: Array<DebugTelemetryRequestItem>;
  /**
   * Out-of-memory kills of the app's instances in the window, newest first.
   */
  oom_kills?: Array<DebugStatusOOMKill>;
  oom_kills_total?: number;
  /**
   * The remedy for the newest out-of-memory kill, one suggestion per line.
   */
  oom_fix?: string;
  tracing_enabled: boolean;
  generated_at: string;
};

