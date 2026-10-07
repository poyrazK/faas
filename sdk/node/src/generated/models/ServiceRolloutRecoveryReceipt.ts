/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable exact service abort request. Acceptance does not confirm traffic restoration, gateway acknowledgement or request drain. Poll the exact deployment's service_rollout_handoff.
 */
export type ServiceRolloutRecoveryReceipt = {
  deployment_id: string;
  predecessor_deployment_id: string;
  request_id: string;
  status: 'accepted';
};

