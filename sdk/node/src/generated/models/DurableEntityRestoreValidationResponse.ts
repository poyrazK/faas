/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type DurableEntityRestoreValidationResponse = {
  bundle_sha256?: string;
  isolation?: 'networkless';
  valid: boolean;
  deployment_id: string;
  expected_version: number;
  source_version: number;
};

