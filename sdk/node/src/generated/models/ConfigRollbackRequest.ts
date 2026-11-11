/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The config revision whose variables a rollback restores.
 */
export type ConfigRollbackRequest = {
  revision: number;
  /**
   * Current configuration revision reviewed before rollback.
   */
  expected_revision?: number;
  /**
   * Immutable reviewed app identity.
   */
  expected_app_id?: string;
  /**
   * Immutable reviewed named environment identity.
   */
  expected_environment_id?: string;
  /**
   * Original reviewed rollback request identity.
   */
  request_id?: string;
  /**
   * Private proof required for receipt recovery.
   */
  receipt_proof?: string;
};

