/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-owned policy for managed exclusive operations. Apps and selected Jobs share the account policy namespace; Jobs require account scope and cannot use an app project environment.
 */
export type ExclusiveOperationPolicy = {
  name?: string;
  scope: 'account' | 'platform_tenant';
  environment_id?: string;
  member_app_ids?: Array<string>;
  member_job_ids?: Array<string>;
  contention: 'queue' | 'reject' | 'join_existing';
  lease_seconds: number;
  max_attempt_seconds: number;
  max_attempts?: number;
  retry_after_seconds?: number;
};

