/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application validator verdict and deployment pin; does not reserve or commit entity state.
 */
export type DurableEntityRestoreValidationResponse = {
  bundle_sha256?: string;
  isolation?: 'networkless';
  valid: boolean;
  deployment_id: string;
  expected_version: number;
  source_version: number;
};

