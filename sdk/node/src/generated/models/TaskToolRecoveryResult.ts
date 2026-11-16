/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable atomic traffic-commit receipt. It binds the consumed owner approval to the exact deployments and evidence, and remains inspectable after deadline expiry or revocation. It never authorizes another write or certifies current health.
 */
export type TaskToolRecoveryResult = {
  intent_id: string;
  operation_id: string;
  handover_id: string;
  qualification_sha256: string;
  revision: number;
  action: 'restore';
  target_deployment_id: string;
  previous_serving_deployment_id: string;
  evidence_sha256: string;
  checked_at: string;
  committed_at: string;
};

