/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sanitized verification verdict; excludes credentials, URLs, response headers and bodies.
 */
export type MigrationVerificationCheck = {
  name: string;
  status: 'passed' | 'failed' | 'pending';
  detail: string;
};

