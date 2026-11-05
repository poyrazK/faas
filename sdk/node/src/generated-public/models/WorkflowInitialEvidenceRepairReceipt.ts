/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Recorded result of an initial recovery evidence repair.
 */
export type WorkflowInitialEvidenceRepairReceipt = {
  request_id: string;
  reason: string;
  actor_kind: string;
  actor_id: string;
  repaired_at: string;
  outcome: 'succeeded' | 'failed' | 'cancelled';
};

