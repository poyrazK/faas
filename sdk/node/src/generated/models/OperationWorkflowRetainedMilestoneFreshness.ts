/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Evidence age evaluated at the shared server timestamp. Expiry is inclusive; missing evidence omits expiry and age fields. Remaining seconds round up while eligible and are zero after expiry.
 */
export type OperationWorkflowRetainedMilestoneFreshness = {
  max_age_seconds: number;
  evaluated_at: string;
  expires_at?: string;
  age_seconds?: number;
  remaining_seconds?: number;
};

