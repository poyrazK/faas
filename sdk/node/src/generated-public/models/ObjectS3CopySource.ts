/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public copy-only authority for an owned source bucket. Placement and grant epoch are private.
 */
export type ObjectS3CopySource = {
  source_bucket_id: string;
  /**
   * Literal allowed source key prefix, bounded to 1024 UTF-8 bytes.
   */
  prefix: string;
  created_at: string;
  updated_at: string;
};

