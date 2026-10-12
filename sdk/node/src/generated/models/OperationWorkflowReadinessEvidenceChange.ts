/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessEvidence } from './OperationWorkflowReadinessEvidence.js';
/**
 * Individual comparison; absence is unavailable unless complete inventories establish blocker clearance.
 */
export type OperationWorkflowReadinessEvidenceChange = {
  /**
   * Evidence change classification.
   */
  kind: 'observed' | 'added' | 'changed' | 'reassigned' | 'required' | 'verified' | 'invalidated' | 'expired' | 'awaiting_verification' | 'fulfilled' | 'unavailable' | 'cleared' | 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch';
  before?: OperationWorkflowReadinessEvidence;
  after?: OperationWorkflowReadinessEvidence;
};

