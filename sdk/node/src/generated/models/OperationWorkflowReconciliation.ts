/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application comparison of authoritative business state and revision against the retained workflow projection.
 */
export type OperationWorkflowReconciliation = {
  workflow: string;
  instance_id: string;
  authoritative_state: string;
  /**
   * Opaque application business revision; distinct from the SDK report counter. UTF-8 byte bound.
   */
  source_revision: string;
  expected_report_revision: number;
  contract_version: number;
  observed_state?: string;
  observed_report_revision: number;
  observed_contract_version: number;
  status: 'in_sync' | 'report_missing' | 'report_behind' | 'report_ahead' | 'state_mismatch' | 'contract_version_mismatch';
};

