/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One platform flag decided for one account. Available is the decision and the plan entitlement of the released capability.
 */
export type OperatorPlatformFlagDecision = {
  flag: string;
  account_id: string;
  plan: string;
  enabled: boolean;
  reason: 'unknown_flag' | 'flag_disabled' | 'account_missing' | 'rule_match' | 'default';
  rule_id?: string;
  bucket?: number;
  version: number;
  entitled?: boolean;
  available: boolean;
};

