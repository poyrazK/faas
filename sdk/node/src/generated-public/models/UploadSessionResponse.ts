/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current resumable upload state. The received_bytes value is the next Upload-Offset a client should use.
 */
export type UploadSessionResponse = {
  upload_id: string;
  app_slug: string;
  chunk_size: number;
  total_size: number;
  received_bytes: number;
  status: 'open' | 'committed' | 'cancelled' | 'expired';
  expires_at: string;
  deployment_id?: string | null;
};

