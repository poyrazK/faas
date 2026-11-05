/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selected binding resource and immutable operation planned for a verification receipt.
 */
export type BindingVerificationPlanStep = {
  target_deployment_id?: string;
  type: string;
  name: string;
  binding: string;
  scope: string;
  queue_environment?: string;
  action: string;
  reason?: string;
  required_evidence?: string;
  checked_at?: string;
  prefix?: string;
  required_mode?: string;
  probe_mode?: string;
  operations?: Array<string>;
};

