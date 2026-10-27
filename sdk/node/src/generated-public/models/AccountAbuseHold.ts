/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Present while the account is on an abuse hold (ADR-361): outbound traffic from its workloads matched a scanning or abuse pattern, so nothing runs or deploys until an operator releases it.
 */
export type AccountAbuseHold = {
  reason: 'egress_fanout' | 'egress_flood' | 'operator';
  held_at: string;
};

