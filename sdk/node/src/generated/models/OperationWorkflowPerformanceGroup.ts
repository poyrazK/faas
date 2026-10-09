/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type OperationWorkflowPerformanceGroup = {
  dimension: 'state_time' | 'blocked_time' | 'verification_wait' | 'state' | 'blocker' | 'verification_owner';
  contract_version?: number;
  state?: string;
  operation?: string;
  code?: string;
  owner?: string;
  unassigned?: boolean;
};

