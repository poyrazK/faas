/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Verified private result reference, from a managed object or direct Job upload.
 */
export type OperationResultArtifact = {
  id: string;
  name: string;
  /**
   * Managed obj:// source or opaque operation://<operation UUID>/artifacts/<artifact UUID> direct-upload reference; never a signed URL or physical storage key.
   */
  uri: string;
  size_bytes: number;
  sha256: string;
  expires_at?: string;
};

