/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioColdPreparation } from './ScenarioColdPreparation.js';
import type { ScenarioColdStartSpec } from './ScenarioColdStartSpec.js';
/**
 * Sealed cold-start inventory with immutable recipe identity and independent host receipts. Raw timing jitter and runtime IDs are excluded from behavioral equality.
 */
export type ScenarioColdStartEvidence = {
  version: 1;
  run_id: string;
  spec: ScenarioColdStartSpec;
  plan_sha256: string;
  status: 'complete' | 'incomplete';
  preparations: Array<ScenarioColdPreparation>;
};

