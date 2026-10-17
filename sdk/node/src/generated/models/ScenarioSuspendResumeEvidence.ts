/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioSuspendResumeReceipt } from './ScenarioSuspendResumeReceipt.js';
import type { ScenarioSuspendResumeSpec } from './ScenarioSuspendResumeSpec.js';
/**
 * Versioned lifecycle inventory. Complete receipts preserve identity while comparisons exclude timing jitter; missing proof remains inconclusive.
 */
export type ScenarioSuspendResumeEvidence = {
  version: 1;
  run_id: string;
  spec: ScenarioSuspendResumeSpec;
  plan_sha256: string;
  status: 'pending' | 'complete' | 'incomplete';
  receipts: Array<ScenarioSuspendResumeReceipt>;
};

