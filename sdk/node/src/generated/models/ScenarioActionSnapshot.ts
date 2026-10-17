/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioCapturedAction } from './ScenarioCapturedAction.js';
import type { ScenarioColdStartEvidence } from './ScenarioColdStartEvidence.js';
import type { ScenarioManagedWorkEvidence } from './ScenarioManagedWorkEvidence.js';
import type { ScenarioSimulationAttempt } from './ScenarioSimulationAttempt.js';
import type { ScenarioSuspendResumeEvidence } from './ScenarioSuspendResumeEvidence.js';
/**
 * Bounded evidence for a sealed action window. Version 1 covers managed webhooks. Version 2 adds declared simulated relay attempts and durable host recorder coverage. Incomplete snapshots never establish equivalent behavior.
 */
export type ScenarioActionSnapshot = {
  suspend_resume?: ScenarioSuspendResumeEvidence;
  cold_start?: ScenarioColdStartEvidence;
  managed_work?: ScenarioManagedWorkEvidence;
  /**
   * Required in simulation scope. Durable host recorder and closing barrier contract.
   */
  recorder_version?: 1;
  /**
   * Retained serving-VM recorder sessions. Every simulation workload must have acknowledged recorder coverage.
   */
  recorder_sessions?: number;
  pending_recorder_sessions?: number;
  incomplete_recorder_sessions?: number;
  version: 1 | 2;
  scope: 'managed_operation_webhooks' | 'managed_webhooks_and_simulated_outbound';
  run_id: string;
  status: 'complete' | 'pending' | 'incomplete';
  /**
   * Present only after the managed-effect window has closed.
   */
  sealed_at?: string;
  workloads: Array<string>;
  intents: Array<ScenarioCapturedAction>;
  /**
   * Present in version 2 snapshots. HTTP and Resend simulation contract version.
   */
  simulation_version?: 1;
  /**
   * Canonical immutable fixture plan identity. Required for simulation scope.
   */
  simulation_sha256?: string;
  simulated_attempts?: Array<ScenarioSimulationAttempt>;
  pending_simulation_attempts?: number;
  incomplete_simulation_attempts?: number;
  captured_count: number;
  pending_managed_work: number;
  incomplete_managed_effects: number;
  /**
   * Unsupported ordinary deliveries blocked by the run policy; these are not successful business effects.
   */
  suppressed_deliveries: number;
  truncated: boolean;
  reason?: string;
};

