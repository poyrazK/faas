/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable, non-secret lifecycle status for one environment approval.
 */
export type ProjectEnvironmentApprovalStatusResponse = {
  approval_id: string;
  environment: string;
  token_kind: 'plan' | 'promotion';
  status: 'pending' | 'expired' | 'consumed';
  created_at: string;
  expires_at: string;
  consumed_at?: string;
};

