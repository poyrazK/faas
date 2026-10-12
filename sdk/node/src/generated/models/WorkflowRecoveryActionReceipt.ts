/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryCancellationRequest } from './WorkflowRecoveryCancellationRequest.js';
import type { WorkflowRecoveryCancellationResponse } from './WorkflowRecoveryCancellationResponse.js';
/**
 * Durable cancellation decision retained for the lifetime of its recovery job. The same action identifier returns the original outcome even after the run advances. Operator identity is resolved from authentication.
 */
export type WorkflowRecoveryActionReceipt = {
  request: WorkflowRecoveryCancellationRequest;
  response: WorkflowRecoveryCancellationResponse;
  captured_at: string;
  actor_kind: string;
  actor_id: string;
};

