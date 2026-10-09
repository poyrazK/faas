/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationRecoveryInspection } from './OperationRecoveryInspection.js';
/**
 * Structural platform eligibility and planned reuse; no evidence that external effects can safely repeat.
 */
export type OperationRecoveryPreview = {
  inspection: OperationRecoveryInspection;
  resolution: 'succeeded' | 'failed' | 'cancelled' | 'safe_to_retry';
  eligible: boolean;
  evidence_required: boolean;
  blockers: Array<string>;
  reused_steps: Array<string>;
  reopened_steps: Array<string>;
  reusable_artifact_ids: Array<string>;
  publish_artifact_ids: Array<string>;
  starts_new_execution: boolean;
  clears_artifact_references: boolean;
};

