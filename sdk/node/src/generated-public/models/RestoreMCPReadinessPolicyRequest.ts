/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical policy restore request guarded by the expected current revision and an audit reason.
 */
export type RestoreMCPReadinessPolicyRequest = {
  scope?: string;
  expected_revision: string;
  reason: string;
};

