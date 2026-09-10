/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectS3Credential } from './ObjectS3Credential.js';
import type { ObjectStorageComputeBindingSecretKeys } from './ObjectStorageComputeBindingSecretKeys.js';
/**
 * Provider-neutral app-to-bucket compute binding.
 */
export type ObjectStorageComputeBinding = {
  id: string;
  bucket_id: string;
  scope: string;
  prefix: string;
  credential: ObjectS3Credential;
  secret_keys: ObjectStorageComputeBindingSecretKeys;
};

