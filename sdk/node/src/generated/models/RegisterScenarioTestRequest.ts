/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioColdStartSpec } from './ScenarioColdStartSpec.js';
import type { ScenarioManagedWorkPlan } from './ScenarioManagedWorkPlan.js';
import type { ScenarioSimulationPlan } from './ScenarioSimulationPlan.js';
import type { ScenarioSuspendResumeSpec } from './ScenarioSuspendResumeSpec.js';
import type { ScenarioTestWorkload } from './ScenarioTestWorkload.js';
/**
 * Complete set of developer sessions in one isolated scenario run.
 */
export type RegisterScenarioTestRequest = {
  suspend_resume?: ScenarioSuspendResumeSpec;
  cold_start?: ScenarioColdStartSpec;
  managed_work?: ScenarioManagedWorkPlan;
  /**
   * Require acknowledgement of contained command execution before deployment. Requires simulation enrollment and an operator-qualified command fleet.
   */
  command_version?: 1;
  simulation?: ScenarioSimulationPlan;
  /**
   * Opt in to runtime outbound containment version 1. Requires capture mode and an operator-qualified fleet. Supports same-run private HTTP and declared simulation adapters; other outbound traffic is blocked.
   */
  network_mode?: 'contained';
  /**
   * Require support for sealed action evidence before deploying. Version 2 requires simulation fixtures. The server must echo this version in its acknowledgement. Requires webhook_mode capture.
   */
  action_evidence_version?: 1 | 2;
  /**
   * Capture managed webhook effects and suppress all other webhook deliveries from these fresh test apps. Requires no existing deployments or work. Does not intercept direct application HTTP calls.
   */
  webhook_mode?: 'capture';
  members: Array<ScenarioTestWorkload>;
};

