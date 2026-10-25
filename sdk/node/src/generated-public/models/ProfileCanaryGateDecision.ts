/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryProfileSignal } from './CanaryProfileSignal.js';
/**
 * Current profiling gate decision for an owned canary stage and its exact stable predecessor.
 */
export type ProfileCanaryGateDecision = {
  status: 'disabled' | 'collecting' | 'passed' | 'regressed' | 'timed_out' | 'overridden' | 'rolled_back';
  reason: string;
  policy_revision: number;
  canary_step: number;
  canary_step_started_at?: string;
  deadline?: string;
  on_timeout?: 'hold' | 'continue';
  auto_rollback: boolean;
  stable_deployment_id?: string;
  signal?: CanaryProfileSignal;
};

