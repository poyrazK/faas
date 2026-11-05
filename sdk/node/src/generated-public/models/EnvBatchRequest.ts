/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * At least one set or delete is required. Keys must be distinct across both collections; plan limits bound collection counts and value sizes.
 */
export type EnvBatchRequest = {
  request_id: string;
  expected_revision: number;
  /**
   * Non-secret variable values to set, omitted from receipts.
   */
  values?: Record<string, string>;
  delete?: Array<string>;
};

