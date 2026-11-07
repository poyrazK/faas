/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable original rollback admission evidence. The source/target identities cannot be reassigned; restoration and current health remain separate.
 */
export type EnvironmentRollbackReceipt = {
  attempt_id: string;
  request_hash: string;
  promotion_id: string;
  promotion_hash: string;
  project_id: string;
  from_environment_id: string;
  environment_id: string;
  accepted_at: string;
};

