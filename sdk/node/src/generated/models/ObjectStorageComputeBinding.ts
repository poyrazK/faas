/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectS3Credential } from './ObjectS3Credential.js';
import type { ObjectStorageComputeBindingSecretKeys } from './ObjectStorageComputeBindingSecretKeys.js';
/**
 * Provider-neutral app-to-bucket compute binding. Rotation responses and binding lists set rotation_pending while the previous key remains valid during a rolling runtime refresh.
 */
export type ObjectStorageComputeBinding = {
  id: string;
  bucket_id: string;
  scope: string;
  prefix: string;
  credential: ObjectS3Credential;
  secret_keys: ObjectStorageComputeBindingSecretKeys;
  /**
   * Previous key is still valid until the rolling refresh has drained old instances.
   */
  rotation_pending?: boolean;
};

