/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body that stores or replaces an app's verification contract.
 */
export type PutVerificationContractRequest = {
  /**
   * The gregale.verify.yaml contract to store, as JSON (version, checks).
   */
  contract: Record<string, any>;
  /**
   * 0 creates only; a positive value replaces only that revision.
   */
  expected_revision?: number;
};

