/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable acceptance of an original reviewed service-abort intent; ACK/drain completion and the current handoff remain separate.
 */
export type ServiceAbortReceipt = {
  attempt_id: string;
  request_hash: string;
  app_id: string;
  deployment_id: string;
  predecessor_deployment_id: string;
  request_id: string;
  /**
   * Default scope or the original named environment slug.
   */
  scope: string;
  /**
   * Original project identity; present for named scopes.
   */
  project_id?: string;
  /**
   * Original environment identity; present for named scopes.
   */
  environment_id?: string;
  accepted_at: string;
};

