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
  /**
   * Organization the decision was made under, when the inspector was given one.
   */
  org_id?: string;
  plan: string;
  enabled: boolean;
  reason: 'unknown_flag' | 'flag_disabled' | 'account_missing' | 'rule_match' | 'default';
  rule_id?: string;
  bucket?: number;
  version: number;
  entitled?: boolean;
  available: boolean;
  /**
   * First rule that matched but was outside its window.
   */
  scheduled_rule_id?: string;
  schedule_state?: 'rule_not_started' | 'rule_ended';
  /**
   * Decision time, when the inspector was asked about another moment than now.
   */
  at?: string;
};

