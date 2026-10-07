/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Temporary bearer capability. Bucket object GET/HEAD/PUT URLs use the branded S3 gateway; a PUT URL dispatches at most one write. Multipart part URLs retain their separate contract. Do not log or persist URLs.
 */
export type ObjectSignedRequest = {
  url: string;
  method: 'GET' | 'HEAD' | 'PUT';
  headers: Record<string, string>;
  expires_at: string;
  /**
   * Durable write receipt for an object PUT URL. Omitted for reads and multipart part URLs.
   */
  upload_id?: string;
};

