/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectS3Credential } from './ObjectS3Credential.js';
/**
 * One-time S3 credential creation response. Configure AWS clients for the returned endpoint, region, and path-style addressing.
 */
export type ObjectS3CredentialSecret = (ObjectS3Credential & {
  readonly secret_access_key: string;
  endpoint: string;
  region: string;
  addressing_style: 'path';
});

