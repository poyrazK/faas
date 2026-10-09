/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Scoped observation of customer ownership, cancellation and current native task timing.
 */
export type OperationJobControlResponse = {
  account_id: string;
  app_id: string;
  platform_tenant_id: string;
  scope: string;
  operation_id: string;
  job_run_id: string;
  generation: number;
  attempt: number;
  cancellation_requested: boolean;
  deadline_at: string;
  lease_expires_at: string;
  observed_at: string;
  poll_after_ms: number;
};

