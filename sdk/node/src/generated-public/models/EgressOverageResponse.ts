/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A billing subject's egress overage choice. With it on, egress past the
 * plan's monthly allowance is billed at millicents_per_gib and counts
 * toward the spend cap; with it off, apps stop at the allowance until
 * the month ends. billed is false while Gregale does not bill egress for
 * the plan.
 *
 */
export type EgressOverageResponse = {
  enabled: boolean;
  billed: boolean;
  included_gib: number;
  millicents_per_gib: number;
};

