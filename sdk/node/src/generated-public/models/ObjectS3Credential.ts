/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bucket-scoped Gregale S3 credential metadata. The secret access key is never included in this shape.
 */
export type ObjectS3Credential = {
  id: string;
  bucket_id: string;
  access_key_id: string;
  label: string;
  permission: 'read' | 'write' | 'read_write';
  status: 'active' | 'revoked';
  created_at: string;
  last_used_at?: string;
  revoked_at?: string;
};

