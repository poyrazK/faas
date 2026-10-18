/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioManagedWorkBinding } from './ScenarioManagedWorkBinding.js';
import type { ScenarioManagedWorkJob } from './ScenarioManagedWorkJob.js';
import type { ScenarioManagedWorkPlan } from './ScenarioManagedWorkPlan.js';
import type { ScenarioWorkerFault } from './ScenarioWorkerFault.js';
/**
 * Bounded participant inventory and managed async completion proof. A complete result requires worker execution, successful receipts and closed admission.
 */
export type ScenarioManagedWorkEvidence = {
  version: 1;
  run_id: string;
  plan_sha256: string;
  status: 'pending' | 'incomplete' | 'complete';
  closing: boolean;
  plan: ScenarioManagedWorkPlan;
  worker_fault?: ScenarioWorkerFault;
  bindings: Array<ScenarioManagedWorkBinding>;
  jobs: Array<ScenarioManagedWorkJob>;
  reason?: string;
};

