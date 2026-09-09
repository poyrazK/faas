/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Least-privilege S3 access and the environment-variable prefix for an app-to-bucket binding.
 */
export type CreateObjectStorageComputeBindingRequest = {
  label?: string;
  permission: 'read' | 'write' | 'read_write';
  prefix?: string;
};

