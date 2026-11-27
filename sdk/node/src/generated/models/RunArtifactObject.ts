/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App-owned object locator governed by the destination bucket retention and access policy.
 */
export type RunArtifactObject = {
  bucket_id: string;
  key: string;
  /**
   * Immutable public version when the backend supplies one. Unversioned objects retain normal app-owned mutation and lifecycle semantics.
   */
  version_id?: string;
};

