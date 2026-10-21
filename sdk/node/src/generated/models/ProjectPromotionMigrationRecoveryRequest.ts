/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator inspection evidence and captured task identity for resuming or resolving a promotion migration incident.
 */
export type ProjectPromotionMigrationRecoveryRequest = {
  request_id: string;
  expected_task_id: string;
  expected_generation: number;
  action: 'resume' | 'resolve';
  /**
   * Required for resolve; must identify a different succeeded verified active promotion in the same destination.
   */
  replacement_promotion_id?: string;
  /**
   * Explicit operator attestation of destination database inspection.
   */
  database_state_checked: boolean;
  /**
   * Required true for resume; attests that the expected expansion is present and backward-compatible.
   */
  schema_compatible: boolean;
  /**
   * SHA256 reference to private inspection evidence. Contents are not uploaded.
   */
  evidence_hash: string;
};

