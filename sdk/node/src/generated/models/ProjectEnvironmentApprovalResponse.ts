/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Short-lived credential for applying the approved plan. The approval token is returned only here.
 */
export type ProjectEnvironmentApprovalResponse = {
  approval_token: string;
  environment: string;
  expires_at: string;
};

