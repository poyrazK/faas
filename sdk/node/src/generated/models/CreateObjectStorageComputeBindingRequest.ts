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
  /**
   * Limit the binding's key to object keys under this literal prefix, such as segments/L0/. It must end in / and may not contain *, ? or [, empty segments, or . and .. segments. Requests outside it, unscoped listings and bucket-wide settings are denied. 403 object_storage_key_scopes_unavailable until key-scoped bindings are released to the account.
   */
  key_prefix?: string;
};

