/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Policy-controlled edge upload route. The edge generates the final object key and never exposes provider placement.
 */
export type ObjectUploadRoute = {
  id: string;
  name: string;
  bucket_id: string;
  key_prefix?: string;
  max_bytes: number;
  allowed_content_types?: Array<string>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

