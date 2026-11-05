/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * At least one set or delete is required. Keys must be distinct across both collections; plan limits bound collection counts and value sizes.
 */
export type EnvBatchRequest = {
  /**
   * Optional immutable app identity; required when expected_environment_id is supplied.
   */
  expected_app_id?: string;
  /**
   * Immutable named-environment identity for an identity-pinned batch; omit for the default scope.
   */
  expected_environment_id?: string;
  /**
   * Optional private random proof required to read this batch receipt; never returned in a receipt. Requires expected_app_id.
   */
  receipt_proof?: string;
  request_id: string;
  expected_revision: number;
  /**
   * Non-secret variable values to set, omitted from receipts.
   */
  values?: Record<string, string>;
  delete?: Array<string>;
};

