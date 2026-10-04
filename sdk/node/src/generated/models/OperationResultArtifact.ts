/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Verified result reference in a private customer-managed object bucket.
 */
export type OperationResultArtifact = {
  id: string;
  name: string;
  /**
   * obj://<app UUID>/<bucket UUID>/<opaque object key>; never a signed URL.
   */
  uri: string;
  size_bytes: number;
  sha256: string;
  expires_at?: string;
};

