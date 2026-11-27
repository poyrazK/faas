/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable ObjectCreated filter targeting only the bucket's owning app.
 */
export type ObjectStorageComputeTrigger = {
  /**
   * Stable manifest identity scoped to the bucket.
   */
  id: string;
  /**
   * Defaults to s3:ObjectCreated:* when omitted or empty.
   */
  events?: Array<'s3:ObjectCreated:*' | 's3:ObjectCreated:Put' | 's3:ObjectCreated:Copy' | 's3:ObjectCreated:CompleteMultipartUpload'>;
  prefix?: string;
  suffix?: string;
};

