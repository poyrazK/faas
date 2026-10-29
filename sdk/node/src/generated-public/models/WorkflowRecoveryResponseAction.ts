/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryRequest } from './EventRecoveryRequest.js';
import type { WorkflowRecoveryCancellationRequest } from './WorkflowRecoveryCancellationRequest.js';
/**
 * One explicit cancellation or saved-failure retry target. Exactly one action request matches the kind and frozen identity.
 */
export type WorkflowRecoveryResponseAction = {
  kind: 'cancel' | 'retry';
  position: number;
  run_id: string;
  cancellation?: WorkflowRecoveryCancellationRequest;
  retry?: EventRecoveryRequest;
};

