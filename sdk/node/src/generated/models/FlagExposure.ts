/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One end user's exposure to a running subject experiment's rule, which SDKs report at most hourly per subject and variant. Only the subject's hash is kept.
 */
export type FlagExposure = {
  flag: string;
  rule_id: string;
  variant: string;
  customer_id: string;
  subject: string;
  at?: string;
};

