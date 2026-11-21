/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * File locator, publication status and integrity metadata visible to the caller.
 */
export type ExecutionFileResponse = {
  id: string;
  status: 'missing' | 'expired' | 'uploading' | 'pending' | 'ready' | 'failed' | 'deleting';
  size_bytes: number;
  sha256?: string;
  /**
   * Relative authenticated content endpoint
   */
  content_url: string;
  expires_at?: string;
  upload_required?: boolean;
};

