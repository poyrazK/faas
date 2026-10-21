/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Short-lived, single-use credential for applying the approved plan. The approval token is returned only to the approving caller or the private browser-review exchange.
 */
export type ProjectEnvironmentApprovalResponse = {
  approval_id: string;
  approval_token: string;
  environment: string;
  token_kind: 'plan' | 'promotion';
  status: 'pending' | 'expired' | 'consumed';
  expires_at: string;
};

